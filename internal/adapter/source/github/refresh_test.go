package ghsource

import (
	"context"
	"slices"
	"testing"

	"github.com/wloophq/wloop/internal/adapter/store/inventory"
	"github.com/wloophq/wloop/internal/core/port"
	dgh "github.com/wloophq/wloop/internal/domain/github"
	wsdomain "github.com/wloophq/wloop/internal/domain/workspace"
	"github.com/wloophq/wloop/internal/github"
	"github.com/wloophq/wloop/internal/meter/metertest"
	"github.com/wloophq/wloop/internal/workspace/fake"
)

func TestRefreshStoresAndReturns(t *testing.T) {
	l := liveFixture(t)
	ref := github.Ref{Owner: "acme", Repo: "api", Number: 900}
	l.gh.add(hostedPR{ref: ref, author: "zoe", state: dgh.StateOpen, updated: l.at, head: "h900", checks: "SUCCESS", mergeable: "MERGEABLE"})
	gen := l.log.Gen()
	ctx, sc := metertest.Start(t, context.Background())
	row, d, err := l.c.Refresh(ctx, ref)
	if err != nil {
		t.Fatal(err)
	}
	if n := metertest.Count(sc, graphqlCalls); n != 1 {
		t.Fatalf("%d GitHub requests, want 1", n)
	}
	if row.Ref != ref || row.Author != "zoe" || row.HeadOid != "h900" || d.Version != inventory.DetailVersion || !d.ReadAt.Equal(l.at) {
		t.Fatalf("returned row %+v, detail %+v", row, d)
	}
	rows, err := l.c.Store.PRs(ctx, []github.Ref{ref})
	if err != nil || rows[ref].HeadOid != "h900" {
		t.Fatalf("stored row %+v: %v", rows[ref], err)
	}
	details, err := l.c.Store.Details(ctx, []github.Ref{ref})
	if err != nil || !details[ref].ReadAt.Equal(l.at) {
		t.Fatalf("stored detail %+v: %v", details[ref], err)
	}
	if c := l.log.Since(gen, port.TopicPRs); !slices.Contains(c.Units, ref.Unit()) {
		t.Fatalf("the read noted %+v", c)
	}
}

func TestRefreshFailsOnAnUnknownPullRequest(t *testing.T) {
	l := liveFixture(t)
	if _, _, err := l.c.Refresh(context.Background(), github.Ref{Owner: "acme", Repo: "api", Number: 999}); err == nil {
		t.Fatal("refreshed a pull request GitHub does not have")
	}
}

// addWorkspace lists one more local workspace of acme/api.
func (l *live) addWorkspace(w wsdomain.Workspace) {
	m := l.c.WS.First().(*fake.Manager)
	w.RepoID, w.HostID = "r1", wsdomain.LocalHost
	m.WorkspaceList = append(m.WorkspaceList, w)
}

func TestReviewWorkspaceLinkedByCollector(t *testing.T) {
	l := liveFixture(t)
	ref := l.requested[0]
	l.addWorkspace(wsdomain.Workspace{ID: "w-rev", DisplayName: "REVIEW " + l.gh.prs[ref].branch(), Branch: "review-checkout"})
	l.fast(t)
	rows, err := l.c.Store.PRs(context.Background(), []github.Ref{ref})
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
	_, sc := l.fast(t)
	idle := metertest.Count(sc, graphqlCalls)
	unstored := github.Ref{Owner: "acme", Repo: "api", Number: 901}
	l.gh.add(hostedPR{ref: unstored, author: "zoe", state: dgh.StateOpen, updated: l.at, head: "h901", checks: "SUCCESS", mergeable: "MERGEABLE"})
	l.addWorkspace(wsdomain.Workspace{ID: "w-found", DisplayName: "REVIEW b901"})
	l.addWorkspace(wsdomain.Workspace{ID: "w-none", DisplayName: "REVIEW nothing"})

	l.gh.reset()
	if _, sc := l.fast(t); metertest.Count(sc, graphqlCalls) != idle+2 || !slices.Equal(l.gh.heads, []string{"b901", "nothing"}) {
		t.Fatalf("first fast run: %d requests (idle %d), searched %v; want one search and one read more", metertest.Count(sc, graphqlCalls), idle, l.gh.heads)
	}
	rows, err := l.c.Store.PRs(context.Background(), []github.Ref{unstored})
	if err != nil || rows[unstored].Workspace.ID != "w-found" {
		t.Fatalf("%s linked to %+v: %v", unstored, rows[unstored].Workspace, err)
	}

	l.gh.reset()
	if _, sc := l.fast(t); metertest.Count(sc, graphqlCalls) != idle || len(l.gh.heads) != 0 {
		t.Fatalf("second fast run: %d requests, searched %v", metertest.Count(sc, graphqlCalls), l.gh.heads)
	}
	l.gh.reset()
	if _, err := l.c.Slow(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(l.gh.heads, []string{"nothing"}) {
		t.Fatalf("the slow run searched %v, want the unlinked branch again", l.gh.heads)
	}

	m := l.c.WS.First().(*fake.Manager)
	m.WorkspaceList[len(m.WorkspaceList)-1].DisplayName = "REVIEW renamed"
	l.gh.reset()
	if _, sc := l.fast(t); metertest.Count(sc, graphqlCalls) != idle+1 || !slices.Equal(l.gh.heads, []string{"renamed"}) {
		t.Fatalf("after a rename: %d requests, searched %v; want the new branch once", metertest.Count(sc, graphqlCalls), l.gh.heads)
	}
}
