package scope

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"
)

type teams map[string][]string

func (t teams) TeamMembers(_ context.Context, org, slug string) ([]string, error) {
	m, ok := t[org+"/"+slug]
	if !ok {
		return nil, errors.New("not found")
	}
	return m, nil
}

func TestResolveUnionsPeopleAndTeams(t *testing.T) {
	cfg := Config{People: []string{"Ana"}, Teams: []string{"acme/search", "acme/missing"}, Repos: []string{"acme/api"}, Watch: []string{"bruno"}}
	r, errs := Resolve(context.Background(), cfg, "me", teams{"acme/search": {"dylan", "ana"}})
	if len(errs) != 1 {
		t.Fatalf("errors: %v", errs)
	}
	if !slices.Equal(r.People, []string{"ana", "dylan"}) || !slices.Equal(r.Teams, []string{"acme/search"}) {
		t.Fatalf("%+v", r)
	}
}

func TestQueries(t *testing.T) {
	r := Resolved{People: []string{"ana", "dylan"}, Teams: []string{"acme/search"}, Repos: []string{"acme/api"}, Watch: []string{"bruno"}, Viewer: "me"}
	since := time.Date(2026, 10, 5, 10, 0, 0, 0, time.UTC)
	qs := r.Queries(since)
	want := []Query{
		{Reason: "author", Q: "is:pr archived:false updated:>=2026-10-05T10:00:00Z author:ana author:dylan author:me"},
		{Reason: "review-requested", Q: "is:pr archived:false updated:>=2026-10-05T10:00:00Z user-review-requested:me"},
		{Reason: "team-review-requested", Q: "is:pr archived:false updated:>=2026-10-05T10:00:00Z team-review-requested:acme/search"},
		{Reason: "watch", Q: "is:pr archived:false updated:>=2026-10-05T10:00:00Z author:bruno"},
		{Reason: "watch", Q: "is:pr archived:false updated:>=2026-10-05T10:00:00Z user-review-requested:bruno"},
		{Reason: "repo", Q: "is:pr archived:false updated:>=2026-10-05T10:00:00Z repo:acme/api"},
	}
	if !slices.Equal(qs, want) {
		t.Fatalf("got\n%v\nwant\n%v", qs, want)
	}
	for _, q := range qs {
		if !strings.Contains(q.Q, " updated:>=2026-10-05T10:00:00Z") || strings.Contains(q.Q, "is:open") {
			t.Fatalf("an incremental query must filter by updated:>= in any state: %s", q.Q)
		}
	}
	for _, q := range r.Queries(time.Time{}) {
		if strings.Contains(q.Q, "updated:") || !strings.Contains(q.Q, " is:open ") {
			t.Fatalf("zero since must list open pull requests only, unfiltered: %s", q.Q)
		}
	}
	if d := r.DiscoveryQueries(since); len(d) != 1 || d[0].Q != "is:pr archived:false updated:>=2026-10-05T10:00:00Z author:ana author:dylan" {
		t.Fatalf("discovery: %v", d)
	}
}

func TestQueriesChunkByLength(t *testing.T) {
	var people []string
	for i := 0; i < 40; i++ {
		people = append(people, strings.Repeat("x", 20)+string(rune('a'+i%26))+string(rune('a'+i/26)))
	}
	r := Resolved{People: people, Viewer: "me"}
	n := 0
	var seen []string
	for _, q := range r.Queries(time.Time{}) {
		if q.Reason != "author" {
			continue
		}
		n++
		if len(q.Q) > MaxQueryLen {
			t.Fatalf("query too long (%d): %s", len(q.Q), q.Q)
		}
		for _, f := range strings.Fields(q.Q) {
			if a, ok := strings.CutPrefix(f, "author:"); ok {
				seen = append(seen, a)
			}
		}
	}
	if n < 4 {
		t.Fatalf("expected the 41 authors split into at least 4 queries, got %d", n)
	}
	want := append(slices.Clone(people), "me")
	slices.Sort(want)
	slices.Sort(seen)
	if !slices.Equal(seen, want) {
		t.Fatalf("every author must appear exactly once across the chunks: got %d terms, want %d", len(seen), len(want))
	}
}

func TestEmptyViewerSkipsViewerQueries(t *testing.T) {
	r, errs := Resolve(context.Background(), Config{People: []string{"ana"}, Teams: []string{"acme/", "acme/a/b"}}, "", nil)
	if len(errs) != 3 {
		t.Fatalf("want an error for the viewer and one per malformed team, got %v", errs)
	}
	for _, q := range r.Queries(time.Time{}) {
		if strings.HasSuffix(q.Q, ":") || strings.Contains(q.Q, ": ") {
			t.Fatalf("dangling qualifier: %s", q.Q)
		}
	}
	if got := r.Reasons("", "other/x", []string{""}); len(got) != 0 {
		t.Fatalf("a ghost author must have no reason: %v", got)
	}
}

func TestReasons(t *testing.T) {
	r := Resolved{People: []string{"ana"}, Teams: []string{"acme/search"}, Repos: []string{"acme/api"}, Watch: []string{"bruno"}, Viewer: "me"}
	got := r.Reasons("ana", "acme/api", []string{"me", "team:acme/search"})
	if !slices.Equal(got, []string{"author", "repo", "review-requested", "team-review-requested"}) {
		t.Fatalf("%v", got)
	}
	if got := r.Reasons("bruno", "other/x", nil); !slices.Equal(got, []string{"watch"}) {
		t.Fatalf("%v", got)
	}
	if got := r.Reasons("outsider", "other/x", []string{"carla"}); len(got) != 0 {
		t.Fatalf("out of scope must have no reason: %v", got)
	}
}

func TestResolveKeepsTheTeamsTheViewerBelongsTo(t *testing.T) {
	cfg := Config{Teams: []string{"acme/search", "Acme/Platform"}}
	r, errs := Resolve(context.Background(), cfg, "Me", teams{"acme/search": {"dylan", "ME"}, "Acme/Platform": {"ana"}})
	if len(errs) != 0 {
		t.Fatalf("errors: %v", errs)
	}
	if !slices.Equal(r.MyTeams, []string{"acme/search"}) {
		t.Fatalf("my teams: %v", r.MyTeams)
	}
}
