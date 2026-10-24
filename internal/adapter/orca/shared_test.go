package orca_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/adapter/orca"
	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/core/turns"
	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/workspace/orca/orcatest"
)

type at time.Time

func (a at) Now() time.Time { return time.Time(a) }

func TestSharedWorkspaceSessionsByUnitOverOrca(t *testing.T) {
	const wt = "r1::/wt/shared"
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	rt := orcatest.Start(t, orcatest.World{
		Repos:     []orcatest.Repo{{ID: "r1", Path: "/src/api", RemoteKey: "github.com/acme/api"}},
		Worktrees: []orcatest.Worktree{{ID: wt, RepoID: "r1", Path: "/wt/shared", DisplayName: "shared", Branch: "refs/heads/main"}},
		Terminals: []orcatest.Terminal{
			{Handle: "term-a", WorktreeID: wt, TabID: "tab-a", Title: "claude", LastOutputAt: now.Add(-time.Hour).UnixMilli()},
			{Handle: "term-b", WorktreeID: wt, TabID: "tab-b", Title: "claude", LastOutputAt: now.UnixMilli()},
		},
	})
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "inv.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	repo := sqlite.New(db)
	a := orca.New(rt.Manager(), orca.Columns{}, func(context.Context) (map[pr.Ref]orca.Link, error) { return nil, nil })
	s := &turns.Service{Store: repo, Registry: repo, Sessions: turns.NewRouter(repo, a), Clock: at(now)}
	ctx := context.Background()
	threadA, threadB := unit.Unit{Provider: "slack", Key: "T000/C001/1.1"}, unit.Unit{Provider: "slack", Key: "T000/C001/2.2"}
	for h, u := range map[string]unit.Unit{"term-a": threadA, "term-b": threadB} {
		if err := repo.SaveSession(ctx, turn.Session{ID: h, Manager: "orca", Handle: h, WorkspaceID: wt, Unit: u, StartedAt: now.Add(-2 * time.Hour)},
			port.Event{Kind: turns.KindSessionLaunched, Key: h, At: now}); err != nil {
			t.Fatal(err)
		}
		if err := repo.BindHandle(ctx, h, "orca", h, "", now.Add(-2*time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	for u, want := range map[unit.Unit]string{threadA: "term-a", threadB: "term-b"} {
		got, ok, err := s.SessionFor(ctx, u, wt)
		if err != nil || !ok || got.Session.ID != want {
			t.Fatalf("%v: %+v %v %v, want %s", u, got, ok, err, want)
		}
	}
	rt.Update(func(w *orcatest.World) { w.Terminals = w.Terminals[1:] })
	a.Refresh()
	if got, ok, err := s.SessionFor(ctx, threadA, wt); ok || err != nil {
		t.Fatalf("a closed terminal: %+v %v %v", got, ok, err)
	}
}
