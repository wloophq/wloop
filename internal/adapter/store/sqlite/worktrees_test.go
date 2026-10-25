package sqlite

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/domain/unit"
	wsdomain "github.com/wloophq/wloop/internal/domain/workspace"
)

func worktreeOf(t *testing.T, w Worktrees, path string) (wsdomain.Custody, bool) {
	t.Helper()
	c, ok, err := w.Worktree(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	return c, ok
}

func TestWorktreesKeep(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	w := newStore(t).db.Worktrees()
	pr7 := unit.Unit{Provider: unit.GitHub, Key: "acme/api#7"}
	at := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	if _, ok := worktreeOf(t, w, "/wt/a"); ok {
		t.Fatal("an unknown worktree is in custody")
	}
	first := wsdomain.Custody{Path: "/wt/a", Repo: "/src/api", Manager: "orca", WorkspaceID: "repo::/wt/a", CreatedBy: "orca", At: at, Units: []unit.Unit{pr7}}
	if err := w.Keep(ctx, first); err != nil {
		t.Fatal(err)
	}
	got, ok := worktreeOf(t, w, "/wt/a")
	if !ok || got.Path != first.Path || got.Repo != first.Repo || got.Manager != "orca" || got.WorkspaceID != first.WorkspaceID ||
		got.CreatedBy != "orca" || !got.At.Equal(at) || !slices.Equal(got.Units, []unit.Unit{pr7}) {
		t.Fatalf("Worktree = %+v %v\nwant %+v", got, ok, first)
	}

	if err := w.Keep(ctx, wsdomain.Custody{Path: "/wt/a", Manager: "desktop", WorkspaceID: "d-1", CreatedBy: "desktop", At: at.Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	got, _ = worktreeOf(t, w, "/wt/a")
	if got.CreatedBy != "orca" || got.Manager != "desktop" || got.WorkspaceID != "d-1" || got.Repo != "/src/api" || !slices.Equal(got.Units, []unit.Unit{pr7}) {
		t.Fatalf("a second Keep: %+v, want the creator and repository kept, the owner taken", got)
	}

	if err := w.Keep(ctx, wsdomain.Custody{Path: "/wt/found", Manager: "orca", At: at}); err != nil {
		t.Fatal(err)
	}
	if err := w.Keep(ctx, wsdomain.Custody{Path: "/wt/found", Manager: "orca", CreatedBy: "orca", At: at}); err != nil {
		t.Fatal(err)
	}
	if got, _ := worktreeOf(t, w, "/wt/found"); got.CreatedBy != "orca" {
		t.Fatalf("a found worktree's creator: %q", got.CreatedBy)
	}
}

func TestWorktreesLinkUnitNeedsTheWorktree(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newStore(t)
	w := s.db.Worktrees()
	pr7 := unit.Unit{Provider: unit.GitHub, Key: "acme/api#7"}
	thread := unit.Unit{Provider: "slack", Key: "T/C/1.1"}
	if err := w.LinkUnit(ctx, "/wt/a", pr7); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := s.db.R().QueryRowContext(ctx, `SELECT count(*) FROM worktree_units`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("linking a worktree not in custody stored %d units %v", n, err)
	}
	if err := w.Keep(ctx, wsdomain.Custody{Path: "/wt/a", Manager: "orca"}); err != nil {
		t.Fatal(err)
	}
	for _, u := range []unit.Unit{pr7, thread, pr7} {
		if err := w.LinkUnit(ctx, "/wt/a", u); err != nil {
			t.Fatal(err)
		}
	}
	got, _ := worktreeOf(t, w, "/wt/a")
	if len(got.Units) != 2 || !slices.Contains(got.Units, pr7) || !slices.Contains(got.Units, thread) {
		t.Fatalf("units %+v, want each linked once", got.Units)
	}
}

func TestWorktreesDropAndList(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newStore(t)
	w := s.db.Worktrees()
	pr7 := unit.Unit{Provider: unit.GitHub, Key: "acme/api#7"}
	pr8 := unit.Unit{Provider: unit.GitHub, Key: "acme/api#8"}
	for _, c := range []wsdomain.Custody{
		{Path: "/wt/b", Manager: "orca", Units: []unit.Unit{pr8}},
		{Path: "/wt/a", Manager: "desktop", Units: []unit.Unit{pr7}},
	} {
		if err := w.Keep(ctx, c); err != nil {
			t.Fatal(err)
		}
	}
	all, err := w.Worktrees(ctx)
	if err != nil || len(all) != 2 || all[0].Path != "/wt/a" || all[1].Path != "/wt/b" ||
		!slices.Equal(all[0].Units, []unit.Unit{pr7}) || !slices.Equal(all[1].Units, []unit.Unit{pr8}) {
		t.Fatalf("Worktrees = %+v %v", all, err)
	}
	if err := w.Drop(ctx, "/wt/a"); err != nil {
		t.Fatal(err)
	}
	if _, ok := worktreeOf(t, w, "/wt/a"); ok {
		t.Fatal("a dropped worktree is still in custody")
	}
	var n int
	if err := s.db.R().QueryRowContext(ctx, `SELECT count(*) FROM worktree_units WHERE path = '/wt/a'`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("Drop left %d units %v", n, err)
	}
	if all, err := w.Worktrees(ctx); err != nil || len(all) != 1 || all[0].Path != "/wt/b" {
		t.Fatalf("after Drop: %+v %v", all, err)
	}
	if err := w.Drop(ctx, "/wt/none"); err != nil {
		t.Fatalf("dropping an unknown worktree: %v", err)
	}
}
