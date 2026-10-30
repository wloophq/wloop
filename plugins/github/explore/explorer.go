package explore

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	src "github.com/wloophq/wloop/pkg/plugin/source"
	github "github.com/wloophq/wloop/plugins/github/api"
)

const (
	userLookupTimeout = 10 * time.Second
	recentMax         = 8
	attempt           = 25 * time.Second
	retryPause        = 2 * time.Second
)

// GitHub is what Explore reads of GitHub: its searches and whether a word
// is a user's login.
type GitHub interface {
	Searcher
	IsUser(ctx context.Context, login string) (bool, error)
}

// Store is what Explore reads and keeps beside GitHub: the pull requests
// the plugin holds, the viewer, and the recent searches.
type Store interface {
	Refs(ctx context.Context) ([]github.Ref, error)
	Viewer(ctx context.Context) (string, error)
	Meta(ctx context.Context, key string) (string, error)
	SetMeta(ctx context.Context, key, value string) error
}

// Budget says whether the budget lets a search run; l is the bucket it
// read.
type Budget func() (ok bool, l github.Limit)

// Explorer runs Explore's searches, one at a time, only when the user asks,
// never below the budget the sweeps keep.
type Explorer struct {
	GitHub GitHub
	Store  Store
	Budget Budget
	// Loop names the loop, as the searches of the repositories it knows
	// say.
	Loop string
	// Attempt bounds one search and Pause separates its two tries; zero
	// is the default.
	Attempt, Pause time.Duration

	exploring sync.Mutex
	users     sync.Map // lowercase login -> whether GitHub knows it as a user
}

// Result is one search: what it read the query as, the pull requests it
// found and the budget it spent.
type Result struct {
	Query      string             `json:"query"`
	Read       string             `json:"read"`
	Total      int                `json:"total"`
	Empty      string             `json:"empty,omitempty"`
	RateLimit  github.RateLimit   `json:"rate_limit"`
	Recent     []string           `json:"recent"`
	Candidates []github.Candidate `json:"candidates"`
}

// MetaRecent is the store's key of the recent searches.
const MetaRecent = "explore_recent"

// Search runs one search of input.
func (x *Explorer) Search(ctx context.Context, input string) (Result, error) {
	if _, err := Parse(ctx, input, Lookup{}); err != nil {
		return Result{}, fmt.Errorf("%w: %v", src.ErrUnfit, err)
	}
	if x.Budget != nil {
		if ok, l := x.Budget(); !ok {
			return Result{}, fmt.Errorf("%w: GitHub budget low (%d of %d) until %s; explore waits for the reset", src.ErrUnavailable,
				l.Remaining, l.Limit, l.ResetAt.Local().Format("15:04"))
		}
	}
	if !x.exploring.TryLock() {
		return Result{}, fmt.Errorf("%w: a search is already running", src.ErrUnavailable)
	}
	defer x.exploring.Unlock()
	ctx = github.WithPriority(ctx, github.PriorityBackground)
	lookup, err := x.lookup(ctx)
	if err != nil {
		return Result{}, err
	}
	parsed, err := Parse(ctx, input, lookup)
	if err != nil {
		return Result{}, fmt.Errorf("%w: %v", src.ErrUnfit, err)
	}
	viewer, err := x.Store.Viewer(ctx)
	if err != nil {
		return Result{}, err
	}
	if viewer != "" && strings.EqualFold(parsed.Author, viewer) {
		return Result{}, fmt.Errorf("%w: Explore leaves out your own pull requests; the My PRs view lists them", src.ErrUnfit)
	}
	retry := Retry{Attempt: orDefault(x.Attempt, attempt), Pause: orDefault(x.Pause, retryPause)}
	var page github.CandidatePage
	asked := parsed.Query
	if parsed.Ref != nil {
		asked = parsed.Ref.URL()
		sctx, cancel := context.WithTimeout(ctx, retry.Attempt)
		page, err = x.GitHub.CandidateByRef(sctx, *parsed.Ref, viewer)
		cancel()
	} else {
		page, err = retry.Search(ctx, x.GitHub, parsed.Query, viewer)
	}
	read := fmt.Sprintf("read %q as %s", strings.Join(strings.Fields(input), " "), parsed.Read)
	hidden := ""
	if err == nil && parsed.Author != "" && page.Total == 0 && len(lookup.Repos) > 0 {
		queries := AuthorInRepos(parsed.Author, lookup.Repos, parsed.Sort, parsed.Keep)
		var more github.CandidatePage
		var failed []error
		if more, failed, err = retry.AuthorInRepos(ctx, x.GitHub, queries, parsed.Sort, viewer); err == nil {
			more.RateLimit.Cost += page.RateLimit.Cost
			page = more
		}
		n := min(len(lookup.Repos), MaxFallbackRepos)
		hidden = fmt.Sprintf(" in the %s %s knows", plural(n, "repository", "repositories"), x.Loop)
		searched := fmt.Sprintf("searched the %s %s knows in %s", plural(n, "repository", "repositories"), x.Loop, plural(len(queries), "search", "searches"))
		if len(page.Candidates) > 0 {
			read += fmt.Sprintf("; GitHub search cannot see %s without a repository; %s", parsed.Author, searched)
		} else {
			read += "; none across GitHub, then " + searched
		}
		if len(failed) > 0 {
			read += fmt.Sprintf("; %d of the %s failed: %v", len(failed), plural(len(queries), "search", "searches"), failed[0])
		}
	}
	if err != nil {
		return Result{}, fmt.Errorf("GitHub search: %w", err)
	}
	res := Result{Query: asked, Read: read, Total: page.Total, RateLimit: page.RateLimit, Candidates: append([]github.Candidate{}, page.Candidates...)}
	switch {
	case len(res.Candidates) > 0:
	case parsed.Author != "":
		res.Empty = parsed.Author + " has no open pull requests you did not open" + hidden
	case parsed.Team != "":
		org, _, _ := strings.Cut(parsed.Team, "/")
		res.Empty = fmt.Sprintf("no open pull request asks %s for a review; GitHub knows a team by its slug, listed at https://github.com/orgs/%s/teams", parsed.Team, org)
	}
	remembered := strings.Join(strings.Fields(input), " ")
	if parsed.Ref != nil {
		remembered = parsed.Ref.URL()
	}
	if res.Recent, err = x.remember(context.WithoutCancel(ctx), remembered); err != nil {
		return Result{}, fmt.Errorf("explore: recent searches: %w", err)
	}
	return res, nil
}

func orDefault(d, def time.Duration) time.Duration {
	if d <= 0 {
		return def
	}
	return d
}

// lookup reads the repositories and owners the store holds, the owners
// with the most pull requests first.
func (x *Explorer) lookup(ctx context.Context) (Lookup, error) {
	refs, err := x.Store.Refs(ctx)
	if err != nil {
		return Lookup{}, err
	}
	l := Lookup{IsUser: x.isUser}
	count := map[string]int{}
	for _, ref := range refs {
		repo := ref.Owner + "/" + ref.Repo
		if !slices.Contains(l.Repos, repo) {
			l.Repos = append(l.Repos, repo)
		}
		if count[ref.Owner] == 0 {
			l.Owners = append(l.Owners, ref.Owner)
		}
		count[ref.Owner]++
	}
	slices.SortStableFunc(l.Owners, func(a, b string) int {
		if count[a] != count[b] {
			return count[b] - count[a]
		}
		return strings.Compare(a, b)
	})
	return l, nil
}

// isUser asks GitHub once per login for the explorer's life; a failure is
// not kept.
func (x *Explorer) isUser(ctx context.Context, login string) (bool, error) {
	key := strings.ToLower(login)
	if v, ok := x.users.Load(key); ok {
		return v.(bool), nil
	}
	lctx, cancel := context.WithTimeout(ctx, userLookupTimeout)
	defer cancel()
	ok, err := x.GitHub.IsUser(lctx, login)
	if err != nil {
		return false, err
	}
	x.users.Store(key, ok)
	return ok, nil
}

// Recent are the last searches, the newest first.
func (x *Explorer) Recent(ctx context.Context) ([]string, error) {
	raw, err := x.Store.Meta(ctx, MetaRecent)
	list := []string{}
	if err != nil || raw == "" {
		return list, err
	}
	if json.Unmarshal([]byte(raw), &list) != nil {
		return []string{}, nil
	}
	return list, nil
}

// remember puts input first among the recent searches, once.
func (x *Explorer) remember(ctx context.Context, input string) ([]string, error) {
	list, err := x.Recent(ctx)
	if err != nil {
		return nil, err
	}
	list = slices.DeleteFunc(list, func(q string) bool { return q == input })
	list = append([]string{input}, list...)
	if len(list) > recentMax {
		list = list[:recentMax]
	}
	b, err := json.Marshal(list)
	if err != nil {
		return nil, err
	}
	return list, x.Store.SetMeta(ctx, MetaRecent, string(b))
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}
