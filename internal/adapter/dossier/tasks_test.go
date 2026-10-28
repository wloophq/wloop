package dossier_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	"github.com/wloophq/wloop/internal/adapter/store/sqlite/sqlitetest"
	"github.com/wloophq/wloop/internal/domain/dossier"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
)

// putTask stores a task holding us, in cat.
func (e *env) putTask(t *testing.T, id task.ID, cat task.Category, us ...unit.Unit) task.Task {
	t.Helper()
	tk := task.Task{ID: id, State: task.State(cat), Category: cat, Title: "Ship " + string(id), Units: us,
		Origin: task.OriginDerived, Created: e.clock.now, Updated: e.clock.now}
	op := task.OpCreate
	if _, err := e.db.Tasks().Task(context.Background(), id); err == nil {
		op = task.OpUpdate
	}
	if err := e.db.Tasks().Apply(context.Background(), []task.Change{{Op: op, Task: tk}}); err != nil {
		t.Fatal(err)
	}
	return tk
}

func TestATurnOnAPRReadsItsTasksDossier(t *testing.T) {
	te := newTurnsEnv(t)
	other := unit.Unit{Provider: "github", Key: "owner/repo#13"}
	te.putTask(t, "t-00000000000000e1", task.CategoryReady, pull, other)
	own := dossier.Owner{Unit: task.UnitOf("t-00000000000000e1")}
	d := te.openFor(t, pull)
	if d.Owner != own || d.Path != filepath.Join(te.state, "dossiers", "task", "t-00000000000000e1#") {
		t.Fatalf("a turn on the pull request opened %+v, want its task's dossier", d)
	}
	if got := te.openFor(t, other); got.Path != d.Path {
		t.Fatalf("a turn on the task's second pull request opened %s, want %s", got.Path, d.Path)
	}
	writeText(t, filepath.Join(d.Path, "log", "20261022T120000Z-session.md"), "review of #13\n")
	if v := te.check(t, other, "turn 1"); len(v) != 0 {
		t.Fatalf("violations %v", v)
	}
	if got := git(t, d.Path, "log", "-1", "--format=%s"); got != "turn 1" {
		t.Fatalf("the turn on #13 did not commit the task's dossier: %q", got)
	}
	for _, u := range []unit.Unit{pull, other} {
		if _, ok, err := te.svc.Get(context.Background(), dossier.Owner{Unit: u}); ok || err != nil {
			t.Errorf("%s has a dossier of its own: %v", u.Label(), err)
		}
	}
	if got := te.openFor(t, thread); got.Owner != (dossier.Owner{Unit: thread}) {
		t.Errorf("a thread no task holds opened %v, want its own dossier", got.Owner)
	}
	if got := te.openFor(t, own.Unit); got.Path != d.Path {
		t.Errorf("a turn on the task opened %s, want %s", got.Path, d.Path)
	}
}

func TestADossierArchivesWhenItsTaskEnds(t *testing.T) {
	te := newTurnsEnv(t)
	const id = task.ID("t-00000000000000e2")
	te.putTask(t, id, task.CategoryReady, pull)
	d := te.openFor(t, pull)
	writeText(t, filepath.Join(d.Path, "log", "20261022T120000Z-session.md"), "decided on 3 retries\n")
	te.check(t, pull, "turn 1")
	ctx := context.Background()
	own := dossier.Owner{Unit: task.UnitOf(id)}
	state := func() dossier.Dossier {
		t.Helper()
		got, ok, err := te.svc.Get(ctx, own)
		if err != nil || !ok {
			t.Fatalf("the task's dossier: %v %v", ok, err)
		}
		return got
	}
	if err := te.res.Units(ctx, pull, own.Unit); err != nil || len(te.asker.asked) != 0 || state().Status != dossier.Active {
		t.Fatalf("an open task's dossier was closed: %v", err)
	}
	te.putTask(t, id, task.CategoryDone, pull)
	te.asker.answers = []string{closingAnswer}
	if err := te.res.Units(ctx, own.Unit); err != nil {
		t.Fatal(err)
	}
	if len(te.asker.asked) != 1 {
		t.Fatalf("%d closing calls for a task with no item, want 1", len(te.asker.asked))
	}
	if got := state(); got.Status != dossier.Resolved || !got.ResolvedAt.Equal(te.clock.now) {
		t.Fatalf("after the task ended: %+v", got)
	}
	te.clock.now = te.clock.now.Add(31 * day)
	if a, _ := te.sweep(t); a != 1 {
		t.Fatalf("archived %d, want the ended task's dossier", a)
	}
	if !exists(dossier.Tarball(te.state, state().RelPath)) || exists(d.Path) {
		t.Fatalf("archive present %v, directory present %v", exists(dossier.Tarball(te.state, state().RelPath)), exists(d.Path))
	}
	te.putTask(t, id, task.CategoryReady, pull)
	if err := te.res.Units(ctx, pull); err != nil {
		t.Fatal(err)
	}
	if got := state(); got.Status != dossier.Active || readText(t, filepath.Join(d.Path, "log", "20261022T120000Z-session.md")) != "decided on 3 retries\n" {
		t.Fatalf("the reopened task's dossier %+v", got)
	}
}

func TestAMergedDossierOutlivesTheTurnsOfItsTarget(t *testing.T) {
	te := newTurnsEnv(t)
	te.putTask(t, "t-00000000000000e3", task.CategoryReady, pull)
	d := te.openFor(t, pull)
	merged := filepath.Join(d.Path, dossier.MergedDir, "t-00000000000000e4")
	writeText(t, filepath.Join(merged, "notes.md"), "the merged task's notes\n")
	git(t, filepath.Dir(merged), "init", "-q", filepath.Base(merged))
	if v := te.check(t, pull, "turn 1"); len(v) != 0 {
		t.Fatalf("violations %v", v)
	}
	if got := readText(t, filepath.Join(merged, "notes.md")); got != "the merged task's notes\n" {
		t.Fatalf("the merged notes became %q", got)
	}
	if _, err := os.Stat(filepath.Join(merged, ".git")); err != nil {
		t.Fatalf("the merged repository: %v", err)
	}
}

// TestAnArchivedMergedDossierIsPurged: a merged archive is never revived, even while the task it was merged into
// is open.
func TestAnArchivedMergedDossierIsPurged(t *testing.T) {
	e := newEnv(t)
	e.putTask(t, "t-00000000000000e5", task.CategoryReady)
	o := dossier.Owner{Unit: task.UnitOf("t-00000000000000e5"), Merged: "t-00000000000000e6"}
	rel, err := dossier.Rel(o)
	if err != nil {
		t.Fatal(err)
	}
	tarball := dossier.Tarball(e.state, rel)
	writeText(t, tarball, "archive")
	d := dossier.Dossier{Owner: o, RelPath: rel, Status: dossier.Archived, ArchivedAt: e.clock.now, PurgeAfter: e.clock.now.Add(day)}
	if err := e.db.Dossiers().Put(context.Background(), d); err != nil {
		t.Fatal(err)
	}
	if _, p := e.sweep(t); p != 0 || !exists(tarball) {
		t.Fatalf("purged %d before its time", p)
	}
	e.clock.now = e.clock.now.Add(2 * day)
	if _, p := e.sweep(t); p != 1 || exists(tarball) {
		t.Fatalf("purged %d, archive left %v", p, exists(tarball))
	}
}

func TestAMigratedEndedTasksDossierGetsNoClosingCall(t *testing.T) {
	const hex = "00000000000000e7"
	te := turnsOver(newEnvOver(t, func(path string) {
		sqlitetest.At(t, path, 6,
			`INSERT INTO entries (id, kind, state, origin, created, updated, rev) VALUES ('e-`+hex+`', 'capture', 'done', 'user', 't', '2026-10-04T09:00:00Z', 1)`,
			`INSERT INTO dossiers (owner, rel_path, status) VALUES ('entry/e-`+hex+`', 'entry/e-`+hex+`#', 'active')`)
		if _, err := sqlite.Migrate(context.Background(), path, sqlite.MigrateOptions{Backups: filepath.Join(filepath.Dir(path), "backups"), Now: func() time.Time { return time.Time{} }}); err != nil {
			t.Fatal(err)
		}
	}))
	dir, err := dossier.Dir(te.state, dossier.Owner{Unit: task.UnitOf("t-" + hex)})
	if err != nil {
		t.Fatal(err)
	}
	if err := te.files.Ensure(context.Background(), dir); err != nil {
		t.Fatal(err)
	}
	te.asker.answers = []string{closingAnswer}
	if err := te.res.All(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(te.asker.asked) != 0 {
		t.Fatalf("%d closing calls for a task that ended before the upgrade", len(te.asker.asked))
	}
}

func TestAUnitDossierJoinsTheTaskThatPicksTheUnitUp(t *testing.T) {
	te := newTurnsEnv(t)
	ctx := context.Background()
	te.turns.Moves = func(ctx context.Context) error {
		moves, err := te.db.DossierMoves(ctx)
		if err != nil {
			return err
		}
		root := dossier.Root(te.state)
		for _, m := range moves {
			if err := os.MkdirAll(filepath.Dir(filepath.Join(root, m.To)), 0o700); err != nil {
				return err
			}
			if err := os.Rename(filepath.Join(root, m.From), filepath.Join(root, m.To)); err != nil {
				return err
			}
			if err := te.db.DossierMoved(ctx, m.Seq); err != nil {
				return err
			}
		}
		return nil
	}
	d := te.openFor(t, thread)
	if d.Owner != (dossier.Owner{Unit: thread}) {
		t.Fatalf("a thread no task holds opened %v", d.Owner)
	}
	writeText(t, filepath.Join(d.Path, "log", "20261022T120000Z-session.md"), "asked about the rollout\n")
	if v := te.check(t, thread, "turn 1"); len(v) != 0 {
		t.Fatalf("violations %v", v)
	}
	const id = task.ID("t-00000000000000e8")
	te.putTask(t, id, task.CategoryReady, thread)
	next := te.openFor(t, thread)
	if next.Owner != (dossier.Owner{Unit: task.UnitOf(id)}) {
		t.Fatalf("the next turn on the thread opened %v, want its task's dossier", next.Owner)
	}
	if got := readText(t, filepath.Join(next.Path, "log", "20261022T120000Z-session.md")); got != "asked about the rollout\n" {
		t.Fatalf("the task's dossier reads %q, want the thread's log", got)
	}
	if _, ok, err := te.svc.Get(ctx, dossier.Owner{Unit: thread}); ok || err != nil {
		t.Fatalf("the thread keeps a dossier row of its own: %v", err)
	}
	if exists(d.Path) {
		t.Fatalf("the thread's directory %s is left behind", d.Path)
	}
}
