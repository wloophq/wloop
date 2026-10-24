package explore

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	dgh "github.com/wloophq/wloop/internal/domain/github"
	"github.com/wloophq/wloop/internal/github"
)

func TestParse(t *testing.T) {
	const base = "is:pr is:open archived:false -author:@me "
	for in, want := range map[string]string{
		"acme/api":                  base + "repo:acme/api sort:updated-asc",
		"  @zoe ":                   base + "author:zoe sort:updated-asc",
		"org:acme label:bug":        base + "org:acme label:bug sort:updated-asc",
		"org:acme sort:created-asc": base + "org:acme sort:created-asc",
		"fix  login\tflow":          base + "fix login flow sort:updated-asc",
		`"exact phrase" in:title`:   base + `"exact phrase" in:title sort:updated-asc`,
	} {
		got, err := Parse(context.Background(), in, Lookup{})
		if err != nil || got.Ref != nil || got.Query != want {
			t.Errorf("Parse(%q) = %+v, %v; want %q", in, got, err, want)
		}
	}
	got, err := Parse(context.Background(), "https://github.com/acme/api/pull/7/files", Lookup{})
	if err != nil || got.Ref == nil || *got.Ref != (github.Ref{Owner: "acme", Repo: "api", Number: 7}) {
		t.Fatalf("a pull request URL: %+v %v", got, err)
	}
	for _, bad := range []string{"", "   ", "a\x00b", "fix\u202elogin", strings.Repeat("x", 193)} {
		if got, err := Parse(context.Background(), bad, Lookup{}); err == nil {
			t.Errorf("Parse(%q) accepted: %+v", bad, got)
		}
	}
	if _, err := Parse(context.Background(), strings.Repeat("x", 192), Lookup{}); err != nil {
		t.Errorf("the longest search GitHub takes was refused: %v", err)
	}
}

func TestParseKeepsThePrefixFirstAndCountsCharacters(t *testing.T) {
	const base = "is:pr is:open archived:false -author:@me "
	for in, want := range map[string]string{
		"a OR b":                    base + "a OR b sort:updated-asc",
		"label:resort:x":            base + "label:resort:x sort:updated-asc",
		"author:@me OR -author:@me": base + "author:@me OR -author:@me sort:updated-asc",
	} {
		if got, err := Parse(context.Background(), in, Lookup{}); err != nil || got.Query != want || !strings.HasPrefix(got.Query, base) {
			t.Errorf("Parse(%q) = %+v, %v; want %q", in, got, err, want)
		}
	}
	if _, err := Parse(context.Background(), strings.Repeat("é", 192), Lookup{}); err != nil {
		t.Errorf("192 characters, 384 bytes, refused: %v", err)
	}
	if _, err := Parse(context.Background(), strings.Repeat("é", 193), Lookup{}); err == nil || !strings.Contains(err.Error(), "1 character over") {
		t.Errorf("193 characters: %v", err)
	}
	for _, bad := range []string{"a\u200bb", "\ufeffx", "a\u2060b"} {
		if got, err := Parse(context.Background(), bad, Lookup{}); err == nil {
			t.Errorf("Parse(%q) accepted: %+v", bad, got)
		}
	}
}

type users map[string]bool

func (u users) lookup(asked *[]string) func(context.Context, string) (bool, error) {
	return func(_ context.Context, login string) (bool, error) {
		*asked = append(*asked, login)
		if login == "flaky" {
			return false, errors.New("HTTP 502")
		}
		return u[login], nil
	}
}

func TestParseReadsIntent(t *testing.T) {
	const base = "is:pr is:open archived:false -author:@me "
	var asked []string
	l := Lookup{
		Repos:  []string{"acme/web", "ana/web", "acme/api", "Acme/WEB"},
		Owners: []string{"acme", "ana"},
		IsUser: users{"judy": true, "zoe": true}.lookup(&asked),
	}
	for _, c := range []struct{ in, query, read, who string }{
		{"WEB", base + "repo:acme/web repo:ana/web sort:updated-asc", "repo:acme/web repo:ana/web", ""},
		{"api", base + "repo:acme/api sort:updated-asc", "repo:acme/api", ""},
		{"judy", base + "author:judy sort:updated-asc", "author:judy", "judy"},
		{"@zoe", base + "author:zoe sort:updated-asc", "author:zoe", "zoe"},
		{"@acme/platform", base + "team-review-requested:acme/platform sort:updated-asc", "team-review-requested:acme/platform", "acme/platform"},
		{"invoices", base + "invoices org:acme org:ana sort:updated-asc", "text in org:acme org:ana", ""},
		{"flaky", base + "flaky org:acme org:ana sort:updated-asc", "text in org:acme org:ana", ""},
		{"fix login label:bug", base + "fix login label:bug org:acme org:ana sort:updated-asc", "text in org:acme org:ana", ""},
		{`"exact phrase" in:title`, base + `"exact phrase" in:title org:acme org:ana sort:updated-asc`, "text in org:acme org:ana", ""},
		{"fix user:zoe", base + "fix user:zoe sort:updated-asc", "a GitHub search", ""},
		{"fix REPO:acme/api", base + "fix REPO:acme/api sort:updated-asc", "a GitHub search", ""},
		{"org:acme review-requested:@me", base + "org:acme review-requested:@me sort:updated-asc", "a GitHub search", ""},
		{"label:bug -draft:true", base + "label:bug -draft:true sort:updated-asc", "a GitHub search", ""},
		{"acme/web", base + "repo:acme/web sort:updated-asc", "repo:acme/web", ""},
		{"https://github.com/acme/api/pull/7", "", "the pull request acme/api#7", ""},
	} {
		got, err := Parse(context.Background(), c.in, l)
		if err != nil || got.Query != c.query || got.Read != c.read || got.Author+got.Team != c.who {
			t.Errorf("Parse(%q) = %+v, %v; want %q read as %q", c.in, got, err, c.query, c.read)
		}
	}
	if want := []string{"judy", "invoices", "flaky"}; strings.Join(asked, " ") != strings.Join(want, " ") {
		t.Errorf("GitHub was asked about %v, want %v: a known repository, a qualifier or several words never ask", asked, want)
	}
}

func TestParseLeavesTextUnscopedWithoutOwnersAndKeepsTheLimit(t *testing.T) {
	if got, _ := Parse(context.Background(), "invoices", Lookup{}); got.Query != "is:pr is:open archived:false -author:@me invoices sort:updated-asc" || got.Read != "text across GitHub" {
		t.Fatalf("an empty inventory: %+v", got)
	}
	owners := []string{}
	for i := range 12 {
		owners = append(owners, strings.Repeat(string(rune('a'+i)), 10))
	}
	got, err := Parse(context.Background(), "fix", Lookup{Owners: owners})
	if err != nil || strings.Count(got.Query, " org:") != MaxOwners || len(got.Query) > 250 {
		t.Fatalf("ten owners at most: %+v %v", got, err)
	}
	got, err = Parse(context.Background(), strings.Repeat("x", 150), Lookup{Owners: owners})
	if err != nil || len(got.Query) > 250 || strings.Count(got.Query, " org:") != 2 || got.Read != "text in org:"+owners[0]+" org:"+owners[1] {
		t.Fatalf("the owners that fit: %+v %v", got, err)
	}
}

func TestAuthorInReposPacksTheRepositoriesIntoFewSearches(t *testing.T) {
	var repos []string
	for i := range 60 {
		repos = append(repos, fmt.Sprintf("acme/repository-%02d", i))
	}
	qs := AuthorInRepos("ivan", repos, "created-desc", []string{"updated:>=2026-09-15"})
	seen := 0
	for _, q := range qs {
		if !strings.HasPrefix(q, "is:pr is:open archived:false -author:@me author:ivan repo:") || !strings.HasSuffix(q, " updated:>=2026-09-15 sort:created-desc") || utf8.RuneCountInString(q) > 250 {
			t.Errorf("search %q", q)
		}
		seen += strings.Count(q, " repo:")
	}
	if seen != MaxFallbackRepos || len(qs) > 10 {
		t.Fatalf("%d repositories in %d searches", seen, len(qs))
	}
	if !strings.Contains(qs[0], "repo:acme/repository-00 ") || strings.Contains(strings.Join(qs, " "), "repository-50") {
		t.Fatalf("the first %d repositories, in order: %v", MaxFallbackRepos, qs)
	}
	if qs := AuthorInRepos("ivan", nil, DefaultSort, nil); len(qs) != 0 {
		t.Fatalf("no repositories: %v", qs)
	}
}

func TestParseKeepsTheSortTheUserPicked(t *testing.T) {
	const base = "is:pr is:open archived:false -author:@me "
	l := Lookup{Repos: []string{"acme/web"}, IsUser: users{"zoe": true}.lookup(new([]string))}
	for _, c := range []struct{ in, query, who, sort string }{
		{"zoe", base + "author:zoe sort:updated-asc", "zoe", "updated-asc"},
		{"@zoe sort:created-asc", base + "author:zoe sort:created-asc", "zoe", "created-asc"},
		{"sort:updated-desc zoe", base + "author:zoe sort:updated-desc", "zoe", "updated-desc"},
		{"web sort:created-asc", base + "repo:acme/web sort:created-asc", "", "created-asc"},
		{"@acme/search sort:updated-desc", base + "team-review-requested:acme/search sort:updated-desc", "acme/search", "updated-desc"},
		{"org:acme sort:created-asc label:x", base + "org:acme label:x sort:created-asc", "", "created-asc"},
		{"sort:comments-desc", base + "sort:comments-desc", "", "comments-desc"},
		{"@zoe updated:>=2026-09-15", base + "author:zoe updated:>=2026-09-15 sort:updated-asc", "zoe", "updated-asc"},
		{"updated:>=2026-09-15 web sort:created-asc", base + "repo:acme/web updated:>=2026-09-15 sort:created-asc", "", "created-asc"},
	} {
		got, err := Parse(context.Background(), c.in, l)
		if err != nil || got.Query != c.query || got.Author+got.Team != c.who || got.Sort != c.sort {
			t.Errorf("Parse(%q) = %+v, %v; want %q sorted %s", c.in, got, err, c.query, c.sort)
		}
	}
}

func TestOrderFollowsTheSort(t *testing.T) {
	at := func(d int) time.Time { return time.Date(2026, 10, d, 0, 0, 0, 0, time.UTC) }
	cands := []dgh.Candidate{
		{Ref: github.Ref{Number: 1}, CreatedAt: at(2), UpdatedAt: at(9)},
		{Ref: github.Ref{Number: 2}, CreatedAt: at(1), UpdatedAt: at(5)},
		{Ref: github.Ref{Number: 3}, CreatedAt: at(3), UpdatedAt: at(7)},
	}
	numbers := func(cs []dgh.Candidate) []int {
		var out []int
		for _, c := range cs {
			out = append(out, c.Ref.Number)
		}
		return out
	}
	for sort, want := range map[string][]int{
		"updated-asc": {2, 3, 1}, "updated-desc": {1, 3, 2}, "updated": {1, 3, 2},
		"created-asc": {2, 1, 3}, "created-desc": {3, 1, 2}, "created": {3, 1, 2},
		"comments-desc": {1, 2, 3},
	} {
		got := slices.Clone(cands)
		if cmp := Order(sort); cmp != nil {
			slices.SortStableFunc(got, cmp)
		}
		if !slices.Equal(numbers(got), want) {
			t.Errorf("%s: %v, want %v", sort, numbers(got), want)
		}
	}
}
