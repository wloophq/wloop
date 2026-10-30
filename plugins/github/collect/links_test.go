package collect

import (
	"context"
	"fmt"
	"slices"
	"testing"
	"time"

	sdk "github.com/wloophq/wloop/pkg/plugin"
	src "github.com/wloophq/wloop/pkg/plugin/source"
	"github.com/wloophq/wloop/plugins/github/api"
	"github.com/wloophq/wloop/plugins/github/pr"
)

func TestLinkedPRSameAsQueries(t *testing.T) {
	ctx := context.Background()
	c := newCollector(t, &fakeGH{})
	at := func(n int) api.Ref { return api.Ref{Owner: "acme", Repo: "api", Number: n} }
	for _, p := range []pr.PR{
		{Ref: at(1), State: "OPEN", HeadBranch: "feat/x", UpdatedAt: now},
		{Ref: at(2), State: "OPEN", HeadBranch: "feat/x", UpdatedAt: now.Add(-time.Hour)},
		{Ref: at(3), State: "OPEN", HeadBranch: "feat/x", IsFork: true, UpdatedAt: now.Add(-2 * time.Hour)},
		{Ref: at(4), State: "OPEN", HeadBranch: "fork-only", IsFork: true},
		{Ref: at(5), State: "MERGED", HeadBranch: "done"},
		{Ref: at(6), State: "CLOSED", HeadBranch: "feat/y"},
		{Ref: api.Ref{Owner: "acme", Repo: "web", Number: 9}, State: "OPEN", HeadBranch: "feat/x"},
	} {
		if err := c.Store.UpsertPR(ctx, p); err != nil {
			t.Fatal(err)
		}
	}
	cases := []struct {
		name string
		repo string
		w    src.Workspace
		want int
	}{
		{"linked number", "api", src.Workspace{Units: linkedTo(5)}, 5},
		{"linked number not stored", "api", src.Workspace{Units: linkedTo(99)}, 0},
		{"worktree takes the highest open number", "api", src.Workspace{Branch: "feat/x"}, 3},
		{"worktree push branch wins", "api", src.Workspace{Branch: "wip", PushBranch: "feat/x"}, 3},
		{"main checkout leaves forks out", "api", src.Workspace{Branch: "feat/x", IsMain: true}, 2},
		{"main checkout with only a fork", "api", src.Workspace{Branch: "fork-only", IsMain: true}, 0},
		{"worktree on a fork branch", "api", src.Workspace{Branch: "fork-only"}, 4},
		{"closed pull request has no branch match", "api", src.Workspace{Branch: "feat/y"}, 0},
		{"merged pull request has no branch match", "api", src.Workspace{Branch: "done"}, 0},
		{"unknown branch", "api", src.Workspace{Branch: "nothing"}, 0},
		{"other repository stays apart", "web", src.Workspace{Branch: "feat/x"}, 9},
		{"main checkout matches its own branch, not the push branch", "api", src.Workspace{Branch: "wip", PushBranch: "feat/x", IsMain: true}, 0},
		{"linked number wins on a main checkout", "api", src.Workspace{Branch: "feat/x", IsMain: true, Units: linkedTo(3)}, 3},
		{"review workspace by its display name", "api", src.Workspace{DisplayName: "REVIEW feat/x", Branch: "local"}, 3},
		{"review workspace not by its checkout branch", "api", src.Workspace{DisplayName: "REVIEW other", Branch: "feat/x"}, 0},
	}
	list := make([]local, len(cases))
	for i, tc := range cases {
		list[i] = local{Workspace: tc.w, LinkedPR: linkedPR(tc.w, "acme", tc.repo), owner: "acme", name: tc.repo, branch: c.branchOf(tc.w)}
	}
	l, err := c.buildLinks(ctx, list)
	if err != nil {
		t.Fatal(err)
	}
	for i, tc := range cases {
		p, found := l.of(list[i])
		if got := p.Ref.Number; got != tc.want || found != (tc.want != 0) {
			t.Errorf("%s: got #%d (found %v), want #%d", tc.name, got, found, tc.want)
		}
	}
}

func TestLinkWorkspacesBudget(t *testing.T) {
	f := seeded(t)
	ctx := context.Background()
	var repos []src.Repo
	repoIDs := map[string]bool{}
	var wss []src.Workspace
	add := func(owner, repo string, w src.Workspace) {
		id := owner + "/" + repo
		if !repoIDs[id] {
			repoIDs[id] = true
			repos = append(repos, src.Repo{ID: id, Remote: "github.com/" + id})
		}
		w.ID, w.Repo, w.RepoID, w.Local = fmt.Sprintf("ws-%d", len(wss)), id, id, true
		w.DisplayName = w.ID
		wss = append(wss, w)
	}
	linked := append(append([]api.Ref{}, f.Open[:30]...), f.Closed[:30]...)
	stored, err := f.Store.PRs(ctx, linked)
	if err != nil {
		t.Fatal(err)
	}
	for _, ref := range linked {
		add(ref.Owner, ref.Repo, src.Workspace{Units: []sdk.Unit{ref.Unit()}, Status: stored[ref].Workspace.Status})
	}
	byBranch, err := f.Store.PRs(ctx, f.Open[30:70])
	if err != nil {
		t.Fatal(err)
	}
	for _, ref := range f.Open[30:70] {
		add(ref.Owner, ref.Repo, src.Workspace{Branch: byBranch[ref].HeadBranch, Status: byBranch[ref].Workspace.Status})
	}
	for len(wss) < 137 {
		add("org-1", "repo-1", src.Workspace{Branch: fmt.Sprintf("local-%d", len(wss))})
	}
	c := &Collector{Store: f.Store, WS: &listings{listing: src.Listing{Repos: repos, Workspaces: wss}}, Cfg: configured(testConfig()), Now: func() time.Time { return now }}
	list, ok := c.locals(ctx, &Report{})
	if !ok || len(list) != 137 {
		t.Fatalf("%d local workspaces, ok %v", len(list), ok)
	}
	if err := c.linkWorkspaces(ctx, list, mustLinks(t, c, list), &Report{}); err != nil {
		t.Fatal(err)
	}
	all, err := f.Store.ListPRs(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	if n := len(slices.DeleteFunc(all, func(p pr.PR) bool { return p.Workspace.ID == "" })); n != 100 {
		t.Fatalf("%d pull requests linked, want 100: 60 by number and 40 by branch", n)
	}

	l, err := c.buildLinks(ctx, list)
	if err != nil {
		t.Fatal(err)
	}
	if reads := c.workspaceReads(list, l); len(reads) != 0 {
		t.Fatalf("reads %v of stored pull requests", reads)
	}
	mark := f.Changes.mark()
	if err := c.linkWorkspaces(ctx, list, l, &Report{}); err != nil {
		t.Fatal(err)
	}
	if got := f.Changes.since(mark); len(got) != 0 {
		t.Fatalf("linking 137 unchanged workspaces changed %v", got)
	}
}

func mustLinks(t *testing.T, c *Collector, list []local) *links {
	t.Helper()
	l, err := c.buildLinks(context.Background(), list)
	if err != nil {
		t.Fatal(err)
	}
	return l
}
