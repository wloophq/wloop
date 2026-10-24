package dossier_test

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/core/dossiers"
	coreitems "github.com/wloophq/wloop/internal/core/items"
	"github.com/wloophq/wloop/internal/domain/dossier"
	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/unit"
)

const day = 24 * time.Hour

func (e *env) item(t *testing.T, u unit.Unit, state items.State) {
	t.Helper()
	at := e.clock.now
	it := items.Item{ID: "i1", Unit: u, Kind: items.Request, Summary: "s", Origin: unit.Anchor{Unit: u, Ref: "m:1"},
		Requester: items.Person{Key: "github:someone"}, Basis: items.NoBasis, State: state, Confidence: items.High,
		Provenance: items.Extracted, CreatedAt: at, UpdatedAt: at}
	if state == items.Resolved {
		it.Resolution = items.Done
	}
	if err := e.db.Items().Commit(context.Background(), coreitems.Commit{Items: []items.Item{it}}); err != nil {
		t.Fatal(err)
	}
}

func (e *env) sweep(t *testing.T) (int, int) {
	t.Helper()
	a, p, err := e.svc.Sweep(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return a, p
}

func (e *env) get(t *testing.T, u unit.Unit) dossier.Dossier {
	t.Helper()
	d, ok, err := e.svc.Get(context.Background(), dossier.Owner{Unit: u})
	if err != nil || !ok {
		t.Fatalf("Get %v: %v %v", u, ok, err)
	}
	return d
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return !errors.Is(err, fs.ErrNotExist)
}

func TestDossierRowFollowsTheFiles(t *testing.T) {
	e := newEnv(t)
	d := e.open(t, pull)
	if d.Bytes == 0 || d.Files != 1 || !d.LastWriteAt.Equal(e.clock.now) {
		t.Fatalf("opened row %+v, want notes.md counted at the open time", d)
	}
	e.clock.now = e.clock.now.Add(time.Hour)
	write(t, filepath.Join(d.Path, "sources", "a.md"), 1000)
	write(t, filepath.Join(d.Path, "log", "b.md"), 500)
	e.sweep(t)
	got := e.get(t, pull)
	bytes, files, err := e.files.Usage(context.Background(), d.Path)
	if err != nil {
		t.Fatal(err)
	}
	if got.Bytes != bytes || got.Files != files || files != 3 || !got.LastWriteAt.Equal(e.clock.now) {
		t.Errorf("swept row %+v, want %d bytes, 3 files, written at %v", got, bytes, e.clock.now)
	}
	var rel string
	if err := e.db.R().QueryRowContext(context.Background(), `SELECT rel_path FROM dossiers`).Scan(&rel); err != nil {
		t.Fatal(err)
	}
	if filepath.IsAbs(rel) || strings.Contains(rel, e.state) || rel != filepath.Join("github", "owner", "repo%2312#") {
		t.Errorf("rel_path %q", rel)
	}
	e.clock.now = e.clock.now.Add(time.Hour)
	e.sweep(t)
	if again := e.get(t, pull); !again.LastWriteAt.Equal(got.LastWriteAt) {
		t.Errorf("a sweep with no write moved last_write_at to %v", again.LastWriteAt)
	}
}

func TestDossierPathFollowsTheStateDir(t *testing.T) {
	e := newEnv(t)
	e.open(t, pull)
	moved := filepath.Join(t.TempDir(), "elsewhere")
	svc := &dossiers.Service{Store: e.db.Dossiers(), Files: e.files, Policy: dossier.DefaultPolicy, StateDir: moved, Clock: e.clock}
	d, ok, err := svc.Get(context.Background(), dossier.Owner{Unit: pull})
	if err != nil || !ok {
		t.Fatal(ok, err)
	}
	if want := filepath.Join(moved, "dossiers", "github", "owner", "repo%2312#"); d.Path != want {
		t.Errorf("Path %s, want %s", d.Path, want)
	}
}

func TestActiveDossierIsNeverArchived(t *testing.T) {
	e := newEnv(t)
	e.item(t, pull, items.Open)
	d := e.open(t, pull)
	e.clock.now = e.clock.now.Add(365 * day)
	if a, p := e.sweep(t); a+p != 0 {
		t.Fatalf("archived %d, purged %d", a, p)
	}
	ctx := context.Background()
	if err := e.svc.Resolved(ctx, dossier.Owner{Unit: pull}, e.clock.now.Add(-60*day)); err != nil {
		t.Fatal(err)
	}
	if a, p := e.sweep(t); a+p != 0 {
		t.Fatalf("a resolved dossier whose unit is open: archived %d, purged %d", a, p)
	}
	if got := e.get(t, pull); got.Status != dossier.Active || !exists(d.Path) {
		t.Errorf("status %s, dir present %v", got.Status, exists(d.Path))
	}
}

func (e *env) archived(t *testing.T, u unit.Unit) dossier.Dossier {
	t.Helper()
	e.item(t, u, items.Resolved)
	d := e.open(t, u)
	write(t, filepath.Join(d.Path, "sources", "kept.md"), 42)
	if err := e.files.Commit(context.Background(), d.Path, "turn 1"); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.Resolved(context.Background(), dossier.Owner{Unit: u}, e.clock.now); err != nil {
		t.Fatal(err)
	}
	e.clock.now = e.clock.now.Add(29 * day)
	if a, _ := e.sweep(t); a != 0 {
		t.Fatal("archived before 30 days")
	}
	e.clock.now = e.clock.now.Add(2 * day)
	if a, _ := e.sweep(t); a != 1 {
		t.Fatalf("archived %d after 31 days, want 1", a)
	}
	return e.get(t, u)
}

func TestResolvedDossierArchivesAfter30Days(t *testing.T) {
	e := newEnv(t)
	d := e.archived(t, pull)
	tarball := filepath.Join(e.state, "dossiers", ".archive", "github", "owner", "repo%2312#.tar.gz")
	if !exists(tarball) || exists(d.Path) {
		t.Errorf("tarball present %v, dir present %v", exists(tarball), exists(d.Path))
	}
	if d.Status != dossier.Archived || !d.ArchivedAt.Equal(e.clock.now) || !d.PurgeAfter.Equal(e.clock.now.Add(400*day)) {
		t.Errorf("row %+v", d)
	}
	if exists(filepath.Join(e.state, "dossiers", "github")) {
		t.Error("the emptied provider directory stayed")
	}
	assertPrivate(t, filepath.Join(e.state, "dossiers"))
}

func TestArchivePurgedAfterTheConfiguredPeriod(t *testing.T) {
	e := newEnv(t)
	e.svc.Policy.PurgeAfter = 10 * day
	e.archived(t, pull)
	e.clock.now = e.clock.now.Add(9 * day)
	if _, p := e.sweep(t); p != 0 {
		t.Fatal("purged early")
	}
	e.clock.now = e.clock.now.Add(2 * day)
	if _, p := e.sweep(t); p != 1 {
		t.Fatalf("purged %d, want 1", p)
	}
	if got := e.get(t, pull); got.Status != dossier.Purged {
		t.Errorf("status %s", got.Status)
	}
	if exists(filepath.Join(e.state, "dossiers", ".archive", "github")) {
		t.Error("the tarball stayed")
	}
	if a, p := e.sweep(t); a+p != 0 {
		t.Errorf("a purged dossier swept again: %d %d", a, p)
	}
}

func TestReopenRevives(t *testing.T) {
	e := newEnv(t)
	d := e.archived(t, pull)
	if err := e.svc.Reopened(context.Background(), dossier.Owner{Unit: pull}); err != nil {
		t.Fatal(err)
	}
	got := e.get(t, pull)
	if got.Status != dossier.Active || !got.ArchivedAt.IsZero() || !got.ResolvedAt.IsZero() || !got.PurgeAfter.IsZero() {
		t.Errorf("row %+v", got)
	}
	if raw, err := os.ReadFile(filepath.Join(d.Path, "sources", "kept.md")); err != nil || len(raw) != 42 {
		t.Errorf("kept.md after the restore: %d bytes, %v", len(raw), err)
	}
	if n := git(t, d.Path, "rev-list", "--count", "HEAD"); n != "2" {
		t.Errorf("history has %s commits after the restore, want 2", n)
	}
	if exists(dossier.Tarball(e.state, got.RelPath)) {
		t.Error("the tarball stayed after the restore")
	}
	assertPrivate(t, filepath.Join(e.state, "dossiers"))

	if err := e.svc.Reopened(context.Background(), dossier.Owner{Unit: thread}); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := e.svc.Get(context.Background(), dossier.Owner{Unit: thread}); ok {
		t.Error("Reopened created a dossier")
	}
}

func TestPurgeRechecksTheUnit(t *testing.T) {
	e := newEnv(t)
	e.svc.Policy.PurgeAfter = day
	d := e.archived(t, pull)
	e.item(t, pull, items.Open)
	e.clock.now = e.clock.now.Add(2 * day)
	if _, p := e.sweep(t); p != 0 {
		t.Fatal("purged a dossier whose unit reopened")
	}
	if got := e.get(t, pull); got.Status != dossier.Active || !exists(filepath.Join(d.Path, "sources", "kept.md")) {
		t.Errorf("status %s, kept.md present %v", got.Status, exists(filepath.Join(d.Path, "sources", "kept.md")))
	}
}

func TestOverCapIsReported(t *testing.T) {
	e := newEnv(t)
	e.svc.Policy.SoftCap = 64 << 10
	big := e.open(t, pull)
	e.open(t, thread)
	write(t, filepath.Join(big.Path, "sources", "a.bin"), 80<<10)
	r, err := e.svc.Report(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(r.OverCap, []dossier.Owner{{Unit: pull}}) || r.Dossiers.Count != 2 || r.Dossiers.Bytes < 80<<10 {
		t.Errorf("report %+v", r)
	}
}

func TestOpenRefusesAStoreRowOutsideItsOwner(t *testing.T) {
	e := newEnv(t)
	if err := e.db.Dossiers().Put(context.Background(), dossier.Dossier{Owner: dossier.Owner{Unit: pull}, RelPath: "../../x", Status: dossier.Active}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Open(context.Background(), dossier.Owner{Unit: pull}); err == nil {
		t.Error("Open used a stored path that is not the owner's")
	}
}

func TestArchivedDossierWithoutItsTarballReopensEmpty(t *testing.T) {
	e := newEnv(t)
	d := e.archived(t, pull)
	if err := os.Remove(dossier.Tarball(e.state, d.RelPath)); err != nil {
		t.Fatal(err)
	}
	got, err := e.svc.Open(context.Background(), dossier.Owner{Unit: pull})
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != dossier.Active || !exists(filepath.Join(got.Path, "notes.md")) || exists(filepath.Join(got.Path, "sources", "kept.md")) {
		t.Errorf("reopened %+v", got)
	}
}
