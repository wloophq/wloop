package collect

import (
	"context"
	"slices"
	"testing"

	src "github.com/wloophq/wloop/pkg/plugin/source"
	"github.com/wloophq/wloop/plugins/github/api"
	"github.com/wloophq/wloop/plugins/github/store"
)

func TestRefreshStoresAndReturns(t *testing.T) {
	l := liveFixture(t)
	ref := api.Ref{Owner: "acme", Repo: "api", Number: 900}
	l.gh.add(hostedPR{ref: ref, author: "zoe", state: api.StateOpen, updated: l.at, head: "h900", checks: "SUCCESS", mergeable: "MERGEABLE"})
	mark := l.changes.mark()
	ctx := context.Background()
	var err error
	if sent := l.calls(func() { err = l.c.Refresh(ctx, ref) }); err != nil || sent.graphql.Calls != 1 {
		t.Fatalf("%d GitHub requests, want 1: %v", sent.graphql.Calls, err)
	}
	rows, err := l.c.Store.PRs(ctx, []api.Ref{ref})
	if row := rows[ref]; err != nil || row.Ref != ref || row.Author != "zoe" || row.HeadOid != "h900" {
		t.Fatalf("stored row %+v: %v", row, err)
	}
	details, err := l.c.Store.Details(ctx, []api.Ref{ref})
	if d := details[ref]; err != nil || d.Version != store.DetailVersion || !d.ReadAt.Equal(l.at) {
		t.Fatalf("stored detail %+v: %v", d, err)
	}
	if got := l.changes.since(mark); !slices.Contains(got, ref) {
		t.Fatalf("the read changed %v", got)
	}
}

func TestRefreshFailsOnAnUnknownPullRequest(t *testing.T) {
	l := liveFixture(t)
	if err := l.c.Refresh(context.Background(), api.Ref{Owner: "acme", Repo: "api", Number: 999}); err == nil {
		t.Fatal("refreshed a pull request GitHub does not have")
	}
}

// addWorkspace lists one more local workspace of acme/api.
func (l *live) addWorkspace(w src.Workspace) {
	l.ws.edit(func(ls *src.Listing) { ls.Workspaces = append(ls.Workspaces, onAcmeAPI(w)) })
}

func TestReviewWorkspaceLinkedByCollector(t *testing.T) {
	l := liveFixture(t)
	ref := l.requested[0]
	l.addWorkspace(src.Workspace{ID: "w-rev", DisplayName: "REVIEW " + l.gh.prs[ref].branch(), Branch: "review-checkout"})
	l.fast(t)
	rows, err := l.c.Store.PRs(context.Background(), []api.Ref{ref})
	if err != nil {
		t.Fatal(err)
	}
	if ws := rows[ref].Workspace; ws.ID != "w-rev" || !ws.IsReview {
		t.Fatalf("%s linked to %+v, want the review workspace", ref, ws)
	}
	if len(l.gh.heads) != 0 {
		t.Fatalf("searched branches %v the store has", l.gh.heads)
	}
}

func TestFindByBranchOncePerWorkspace(t *testing.T) {
	l := liveFixture(t)
	_, sent := l.fast(t)
	idle := sent.graphql.Calls
	unstored := api.Ref{Owner: "acme", Repo: "api", Number: 901}
	l.gh.add(hostedPR{ref: unstored, author: "zoe", state: api.StateOpen, updated: l.at, head: "h901", checks: "SUCCESS", mergeable: "MERGEABLE"})
	l.addWorkspace(src.Workspace{ID: "w-found", DisplayName: "REVIEW b901"})
	l.addWorkspace(src.Workspace{ID: "w-none", DisplayName: "REVIEW nothing"})

	l.gh.reset()
	if _, sent := l.fast(t); sent.graphql.Calls != idle+2 || !slices.Equal(l.gh.heads, []string{"b901", "nothing"}) {
		t.Fatalf("first fast run: %d requests (idle %d), searched %v; want one search and one read more", sent.graphql.Calls, idle, l.gh.heads)
	}
	rows, err := l.c.Store.PRs(context.Background(), []api.Ref{unstored})
	if err != nil || rows[unstored].Workspace.ID != "w-found" {
		t.Fatalf("%s linked to %+v: %v", unstored, rows[unstored].Workspace, err)
	}

	l.gh.reset()
	if _, sent := l.fast(t); sent.graphql.Calls != idle || len(l.gh.heads) != 0 {
		t.Fatalf("second fast run: %d requests, searched %v", sent.graphql.Calls, l.gh.heads)
	}
	l.gh.reset()
	if _, err := l.c.Slow(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(l.gh.heads, []string{"nothing"}) {
		t.Fatalf("the slow run searched %v, want the unlinked branch again", l.gh.heads)
	}

	l.ws.edit(func(ls *src.Listing) { ls.Workspaces[len(ls.Workspaces)-1].DisplayName = "REVIEW renamed" })
	l.gh.reset()
	if _, sent := l.fast(t); sent.graphql.Calls != idle+1 || !slices.Equal(l.gh.heads, []string{"renamed"}) {
		t.Fatalf("after a rename: %d requests, searched %v; want the new branch once", sent.graphql.Calls, l.gh.heads)
	}
}
