package cli

import (
	"context"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/meter"
	"github.com/wloophq/wloop/internal/meter/metertest"
	"github.com/wloophq/wloop/internal/workspace"
	"github.com/wloophq/wloop/internal/workspace/orca/orcatest"
)

func TestFindFreshListsAgainBeforeGone(t *testing.T) {
	t.Parallel()
	rt := orcatest.Start(t, orcatest.Live())
	m := rt.Manager()
	e := &Env{WS: workspace.NewSet(workspace.NewCached(m, m, 2*time.Minute))}
	if _, err := e.WS.List(context.Background()); err != nil {
		t.Fatal(err)
	}
	rt.Update(func(w *orcatest.World) {
		w.Worktrees = append(w.Worktrees, orcatest.Worktree{ID: "repo-0::/wt/new", RepoID: "repo-0", Path: "/wt/new"})
	})
	lists := metertest.Limit{Res: meter.Workspace, Op: "rpc worktree.list"}

	ctx, sc := metertest.Start(t, context.Background())
	ws, ok, err := e.findFresh(ctx, "repo-0::/wt/new")
	if err != nil || !ok || ws.Path != "/wt/new" {
		t.Fatalf("a workspace made after the last list: %+v %v %v", ws, ok, err)
	}
	if n := metertest.Count(sc, lists); n != 1 {
		t.Fatalf("finding it listed %d times, want 1", n)
	}

	ctx, sc = metertest.Start(t, context.Background())
	if _, ok, err := e.findFresh(ctx, "repo-0::/wt/gone"); err != nil || ok {
		t.Fatalf("an absent workspace: %v %v", ok, err)
	}
	if n := metertest.Count(sc, lists); n != 1 {
		t.Fatalf("saying it is gone listed %d times, want 1 after the cached one", n)
	}
}
