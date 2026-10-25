package cli

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/adapter/store/sqlite/sqlitetest"
	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/config"
	coregtd "github.com/wloophq/wloop/internal/core/gtd"
	"github.com/wloophq/wloop/internal/domain/dossier"
	"github.com/wloophq/wloop/internal/domain/gtd"
	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/unit"
)

type noItems struct{}

func (noItems) Items(context.Context, unit.Unit) ([]items.Item, error) { return nil, nil }

// TestProjectDossierOnDisk runs the project verbs over a real store and real dossier files: the project's
// dossier is a 0700 git directory under the state directory, kept while the project is open, archived 30 days
// after it is done.
func TestProjectDossierOnDisk(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	root := t.TempDir()
	home := filepath.Join(root, "home")
	state := filepath.Join(home, ".local", "state", brand.Name)
	if err := os.MkdirAll(state, 0o700); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	e := &Env{StateDir: state, Home: home, Now: func() time.Time { return now }, noJobs: true}
	db, err := sqlitetest.Open(filepath.Join(state, "store.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	cfg := config.Config{}
	g := e.entries(db, cfg, noItems{})
	ds := e.serveDossiers(ctx, db, cfg, t.Logf)
	serveProjects(g, db, ds)

	capture, err := g.Capture(ctx, "Draft the RFC", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	res, err := g.Do(ctx, capture.ID, coregtd.Request{Verb: gtd.VerbProject, Args: map[string]string{"title": "Ship the v2 API"}})
	if err != nil || len(res.Lines) > 0 {
		t.Fatalf("project: %+v %v", res, err)
	}
	dir, err := dossier.Dir(state, dossier.Owner{Project: "ship-the-v2-api"})
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("no project dossier: %v", err)
	}
	if info.Mode().Perm() != 0o700 {
		t.Errorf("dossier mode %v, want 0700", info.Mode().Perm())
	}
	if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
		t.Errorf("the dossier has no repository: %v", err)
	}

	now = now.Add(40 * 24 * time.Hour)
	if archived, _, err := ds.Sweep(ctx); err != nil || archived != 0 {
		t.Fatalf("an open project's dossier swept: %d %v", archived, err)
	}
	if _, err := g.Do(ctx, res.Entry.Project, coregtd.Request{Verb: gtd.VerbDone}); err != nil {
		t.Fatal(err)
	}
	d, ok, err := ds.Get(ctx, dossier.Owner{Project: "ship-the-v2-api"})
	if err != nil || !ok || d.Status != dossier.Resolved || !d.ResolvedAt.Equal(now) {
		t.Fatalf("after done: %+v %v %v", d, ok, err)
	}
	now = now.Add(31 * 24 * time.Hour)
	if archived, _, err := ds.Sweep(ctx); err != nil || archived != 1 {
		t.Fatalf("a done project's dossier not archived: %d %v", archived, err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("the archived dossier is still on disk: %v", err)
	}
}
