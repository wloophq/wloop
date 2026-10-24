package ghsource

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/adapter/store/storetest"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	wsdomain "github.com/wloophq/wloop/internal/domain/workspace"
	"github.com/wloophq/wloop/internal/github"
	"github.com/wloophq/wloop/internal/meter"
	"github.com/wloophq/wloop/internal/meter/metertest"
	"github.com/wloophq/wloop/internal/workspace"
	"github.com/wloophq/wloop/internal/workspace/fake"
)

func TestLinkedPRSameAsQueries(t *testing.T) {
	ctx := context.Background()
	c := newCollector(t, &fakeGH{})
	at := func(n int) github.Ref { return github.Ref{Owner: "acme", Repo: "api", Number: n} }
	for _, p := range []dinv.PR{
		{Ref: at(1), State: "OPEN", HeadBranch: "feat/x", UpdatedAt: now},
		{Ref: at(2), State: "OPEN", HeadBranch: "feat/x", UpdatedAt: now.Add(-time.Hour)},
		{Ref: at(3), State: "OPEN", HeadBranch: "feat/x", IsFork: true, UpdatedAt: now.Add(-2 * time.Hour)},
		{Ref: at(4), State: "OPEN", HeadBranch: "fork-only", IsFork: true},
		{Ref: at(5), State: "MERGED", HeadBranch: "done"},
		{Ref: at(6), State: "CLOSED", HeadBranch: "feat/y"},
		{Ref: github.Ref{Owner: "acme", Repo: "web", Number: 9}, State: "OPEN", HeadBranch: "feat/x"},
	} {
		if err := c.Store.UpsertPR(ctx, p); err != nil {
			t.Fatal(err)
		}
	}
	cases := []struct {
		name string
		repo string
		w    wsdomain.Workspace
		want int
	}{
		{"linked number", "api", wsdomain.Workspace{LinkedPR: 5}, 5},
		{"linked number not stored", "api", wsdomain.Workspace{LinkedPR: 99}, 0},
		{"worktree takes the highest open number", "api", wsdomain.Workspace{Branch: "feat/x"}, 3},
		{"worktree push branch wins", "api", wsdomain.Workspace{Branch: "wip", PushBranch: "feat/x"}, 3},
		{"main checkout leaves forks out", "api", wsdomain.Workspace{Branch: "feat/x", IsMain: true}, 2},
		{"main checkout with only a fork", "api", wsdomain.Workspace{Branch: "fork-only", IsMain: true}, 0},
		{"worktree on a fork branch", "api", wsdomain.Workspace{Branch: "fork-only"}, 4},
		{"closed pull request has no branch match", "api", wsdomain.Workspace{Branch: "feat/y"}, 0},
		{"merged pull request has no branch match", "api", wsdomain.Workspace{Branch: "done"}, 0},
		{"unknown branch", "api", wsdomain.Workspace{Branch: "nothing"}, 0},
		{"other repository stays apart", "web", wsdomain.Workspace{Branch: "feat/x"}, 9},
		{"main checkout matches its own branch, not the push branch", "api", wsdomain.Workspace{Branch: "wip", PushBranch: "feat/x", IsMain: true}, 0},
		{"linked number wins on a main checkout", "api", wsdomain.Workspace{Branch: "feat/x", IsMain: true, LinkedPR: 3}, 3},
		{"review workspace by its display name", "api", wsdomain.Workspace{DisplayName: "REVIEW feat/x", Branch: "local"}, 3},
		{"review workspace not by its checkout branch", "api", wsdomain.Workspace{DisplayName: "REVIEW other", Branch: "feat/x"}, 0},
	}
	list := make([]local, len(cases))
	for i, tc := range cases {
		list[i] = local{tc.w, "acme", tc.repo, c.branchOf(tc.w)}
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
	f := storetest.Seeded(t, storetest.Live)
	ctx, sc := metertest.Start(t, context.Background())
	var repos []wsdomain.Repo
	repoIDs := map[string]bool{}
	var wss []wsdomain.Workspace
	add := func(owner, repo string, w wsdomain.Workspace) {
		id := owner + "/" + repo
		if !repoIDs[id] {
			repoIDs[id] = true
			repos = append(repos, wsdomain.Repo{ID: id, RemoteKey: "github.com/" + id})
		}
		w.ID, w.RepoID, w.HostID = fmt.Sprintf("ws-%d", len(wss)), id, wsdomain.LocalHost
		w.DisplayName = w.ID
		wss = append(wss, w)
	}
	linked := append(append([]github.Ref{}, f.Open[:30]...), f.Closed[:30]...)
	stored, err := f.Inventory.PRs(ctx, linked)
	if err != nil {
		t.Fatal(err)
	}
	for _, ref := range linked {
		add(ref.Owner, ref.Repo, wsdomain.Workspace{LinkedPR: ref.Number, Status: stored[ref].Workspace.Status})
	}
	for _, ref := range f.Open[30:70] {
		add(ref.Owner, ref.Repo, wsdomain.Workspace{Branch: stored[ref].HeadBranch, Status: stored[ref].Workspace.Status})
	}
	for len(wss) < 137 {
		add("org-1", "repo-1", wsdomain.Workspace{Branch: fmt.Sprintf("local-%d", len(wss))})
	}
	c := &Collector{Store: f.Inventory, WS: workspace.NewSet(&fake.Manager{RepoList: repos, WorkspaceList: wss}), Sink: "board", Cfg: newCollector(t, &fakeGH{}).Cfg, Now: func() time.Time { return now }}
	list, ok := c.locals(ctx, &Report{})
	if !ok || len(list) != 137 {
		t.Fatalf("%d local workspaces, ok %v", len(list), ok)
	}
	if err := c.linkWorkspaces(ctx, list, mustLinks(t, c, list), &Report{}); err != nil {
		t.Fatal(err)
	}

	sql := metertest.Limit{Res: meter.SQL, Op: "query"}
	before := metertest.Count(sc, sql)
	l, err := c.buildLinks(ctx, list)
	if err != nil {
		t.Fatal(err)
	}
	c.workspaceReads(list, l)
	if n := metertest.Count(sc, sql) - before; n != 2 {
		t.Fatalf("%d read statements building the link index and listing reads at 137 workspaces, want 2\n%s", n, metertest.Report(sc.Tally()))
	}
	before = metertest.Count(sc, sql)
	if err := c.linkWorkspaces(ctx, list, l, &Report{}); err != nil {
		t.Fatal(err)
	}
	if n := metertest.Count(sc, sql) - before; n != 1 {
		t.Fatalf("%d read statements linking 137 workspaces, want the unchanged write's one\n%s", n, metertest.Report(sc.Tally()))
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
