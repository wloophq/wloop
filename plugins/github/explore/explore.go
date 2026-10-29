// Package explore turns what the user types in Explore into one GitHub
// search for open pull requests to help on.
package explore

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	github "github.com/wloophq/wloop/plugins/github/api"
	"github.com/wloophq/wloop/plugins/github/scope"
)

// MaxResults is one page: 100 breaks GitHub's per-query resource limit with
// the candidate fragment, 50 doubles the cost for results nobody scans.
const MaxResults = 30

// MaxOwners bounds the owners free text is scoped to.
const MaxOwners = 10

// MaxFallbackRepos bounds the repositories an author's search is retried in.
const MaxFallbackRepos = 50

const prefix = "is:pr is:open archived:false -author:@me "

// DefaultSort puts the pull request that has waited longest first, so whoever
// asked earlier is not pushed further back.
const DefaultSort = "updated-asc"

var (
	repoOnly  = regexp.MustCompile(`^[A-Za-z0-9-]+/[A-Za-z0-9_.-]+$`)
	loginOnly = regexp.MustCompile(`^@[A-Za-z0-9-]+$`)
	teamOnly  = regexp.MustCompile(`^@[A-Za-z0-9-]+/[A-Za-z0-9_.-]+$`)
	bareWord  = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)
	qualifier = regexp.MustCompile(`^-?[A-Za-z_-]+:`)
	ownerQual = regexp.MustCompile(`(?i)^(repo|org|user):`)
)

// Lookup is what the inventory and GitHub tell Explore about a bare word.
type Lookup struct {
	Repos  []string // owner/name of the repositories the inventory holds
	Owners []string // their owners, the most relevant first
	// IsUser answers whether a word is a GitHub user's login; nil, or an
	// error, reads the word as text.
	IsUser func(ctx context.Context, login string) (bool, error)
}

// Request is one pull request when Ref is set, else a search.
type Request struct {
	Ref    *github.Ref
	Query  string
	Read   string // how the input was read, after "as"
	Author string // the login when the search lists one author's pull requests
	Team   string // org/slug when the search lists what one team is asked to review
	Sort   string // the value of the search's sort: qualifier
	// Keep are the updated: qualifiers, which every search of the request
	// carries, the fallback's too.
	Keep []string
}

func Parse(ctx context.Context, input string, l Lookup) (Request, error) {
	in := strings.Join(strings.Fields(input), " ")
	if in == "" {
		return Request{}, errors.New("type a pull request URL, owner/repo, @login, @org/team or a GitHub search")
	}
	if strings.IndexFunc(in, func(r rune) bool { return unicode.IsControl(r) || unicode.Is(unicode.Cf, r) }) >= 0 {
		return Request{}, errors.New("the search holds control or invisible characters")
	}
	if ref, err := github.ParseURL(in); err == nil {
		return Request{Ref: &ref, Read: "the pull request " + ref.String()}, nil
	}
	in, keep, order := splitModifiers(in)
	req := Request{Sort: DefaultSort, Keep: keep}
	if len(order) > 0 {
		req.Sort = strings.TrimPrefix(order[len(order)-1], "sort:")
	}
	search := func(in string) (string, error) { return query(in, keep, order) }
	switch {
	case repoOnly.MatchString(in):
		in = "repo:" + in
		req.Read = in
	case teamOnly.MatchString(in):
		req.Team = in[1:]
		in = "team-review-requested:" + req.Team
		req.Read = in
	case loginOnly.MatchString(in):
		req.Author = in[1:]
		in = "author:" + req.Author
		req.Read = in
	case bareWord.MatchString(in):
		if repos := knownRepos(in, l.Repos); len(repos) > 0 {
			in = "repo:" + strings.Join(repos, " repo:")
			req.Read = in
		} else if isUser(ctx, in, l.IsUser) {
			req.Author = in
			in = "author:" + in
			req.Read = in
		}
	}
	q, err := search(in)
	if err != nil {
		return Request{}, err
	}
	req.Query = q
	if req.Read != "" {
		return req, nil
	}
	if !freeText(in) {
		req.Read = "a GitHub search"
		return req, nil
	}
	req.Read = "text across GitHub"
	var scoped []string
	for _, o := range l.Owners[:min(len(l.Owners), MaxOwners)] {
		q, err := search(in + " org:" + strings.Join(append(slices.Clone(scoped), o), " org:"))
		if err != nil {
			break
		}
		scoped, req.Query = append(scoped, o), q
	}
	if len(scoped) > 0 {
		req.Read = "text in org:" + strings.Join(scoped, " org:")
	}
	return req, nil
}

// AuthorInRepos is login's search again, split by repository: GitHub search
// hides some accounts' pull requests unless repo: scopes it.
func AuthorInRepos(login string, repos []string, sort string, keep []string) []string {
	var out, group []string
	search := func(group []string) (string, error) {
		return query("author:"+login+" repo:"+strings.Join(group, " repo:"), keep, []string{"sort:" + sort})
	}
	for _, r := range repos[:min(len(repos), MaxFallbackRepos)] {
		if _, err := search(append(slices.Clone(group), r)); err != nil && len(group) > 0 {
			q, _ := search(group)
			out, group = append(out, q), nil
		}
		group = append(group, r)
	}
	if len(group) > 0 {
		q, _ := search(group)
		out = append(out, q)
	}
	return out
}

// Order compares candidates as GitHub's sort does, nil for a sort it cannot
// tell from a candidate.
func Order(sort string) func(a, b github.Candidate) int {
	switch sort {
	case "updated-asc":
		return func(a, b github.Candidate) int { return a.UpdatedAt.Compare(b.UpdatedAt) }
	case "updated", "updated-desc":
		return func(a, b github.Candidate) int { return b.UpdatedAt.Compare(a.UpdatedAt) }
	case "created-asc":
		return func(a, b github.Candidate) int { return a.CreatedAt.Compare(b.CreatedAt) }
	case "created", "created-desc":
		return func(a, b github.Candidate) int { return b.CreatedAt.Compare(a.CreatedAt) }
	}
	return nil
}

// splitModifiers takes the updated: and sort: qualifiers out, so a shorthand
// such as @login still reads as one with a window or a sort picked.
func splitModifiers(in string) (rest string, keep, order []string) {
	var words []string
	for _, f := range strings.Fields(in) {
		switch {
		case strings.HasPrefix(f, "sort:"):
			order = append(order, f)
		case strings.HasPrefix(f, "updated:"):
			keep = append(keep, f)
		default:
			words = append(words, f)
		}
	}
	return strings.Join(words, " "), keep, order
}

func query(in string, keep, order []string) (string, error) {
	if len(order) == 0 {
		order = []string{"sort:" + DefaultSort}
	}
	q := strings.Join(slices.Concat(strings.Fields(prefix+in), keep, order), " ")
	if over := utf8.RuneCountInString(q) - scope.MaxQueryLen; over > 0 {
		unit := "characters"
		if over == 1 {
			unit = "character"
		}
		return "", fmt.Errorf("the search is %d %s over GitHub's limit", over, unit)
	}
	return q, nil
}

// knownRepos lists the repositories named word, each once.
func knownRepos(word string, repos []string) []string {
	var out []string
	for _, r := range repos {
		_, name, ok := strings.Cut(r, "/")
		if ok && strings.EqualFold(name, word) && !slices.ContainsFunc(out, func(o string) bool { return strings.EqualFold(o, r) }) {
			out = append(out, r)
		}
	}
	slices.Sort(out)
	return out
}

func isUser(ctx context.Context, word string, ask func(context.Context, string) (bool, error)) bool {
	if ask == nil || !github.ValidLogin(word) {
		return false
	}
	ok, err := ask(ctx, word)
	return err == nil && ok
}

// freeText is a search with words to match and nothing that picks owners.
func freeText(in string) bool {
	text := false
	for _, f := range strings.Fields(in) {
		switch {
		case ownerQual.MatchString(f):
			return false
		case f == "AND" || f == "OR" || f == "NOT":
		case !qualifier.MatchString(f):
			text = true
		}
	}
	return text
}
