package sqlite

import (
	"context"
	"reflect"
	"testing"
	"time"

	coreitems "github.com/wloophq/wloop/internal/core/items"
	"github.com/wloophq/wloop/internal/domain/dossier"
	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
)

func init() {
	reads = append(reads, "DB.Dossiers", "dossierStore.Get", "dossierStore.List", "dossierStore.Open", "dossierStore.Owner", "DB.AgentWorktrees")
	writes = append(writes, writeCase{
		method: "dossierStore.Put",
		call: func(ctx context.Context, e *noteEnv) error {
			return e.db.Dossiers().Put(ctx, dossier.Dossier{Owner: dossier.Owner{Unit: ghPull}, RelPath: "github/acme/api%2312", Status: dossier.Active})
		},
	})
}

func TestDossierStoreRoundTrips(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, path := openTemp(t)
	at := time.Date(2026, 10, 22, 12, 0, 0, 500, time.UTC)
	d := dossier.Dossier{Owner: dossier.Owner{Unit: unit.Unit{Provider: "slack", Key: "T000/C000/1.2"}}, RelPath: "slack/T000/C000/1.2",
		Status: dossier.Archived, Bytes: 10, Files: 2, LastWriteAt: at, ResolvedAt: at.Add(time.Hour), ArchivedAt: at.Add(2 * time.Hour), PurgeAfter: at.Add(3 * time.Hour)}
	p := dossier.Dossier{Owner: dossier.Owner{Unit: task.UnitOf("t-launch"), Merged: "t-old"}, RelPath: "task/t-launch#/merged/t-old", Status: dossier.Archived}
	for _, x := range []dossier.Dossier{d, p} {
		if err := db.Dossiers().Put(ctx, x); err != nil {
			t.Fatal(err)
		}
	}
	got, ok, err := db.Dossiers().Get(ctx, d.Owner)
	if err != nil || !ok || !reflect.DeepEqual(got, d) {
		t.Errorf("Get = %+v %v %v, want %+v", got, ok, err, d)
	}
	if _, ok, err := db.Dossiers().Get(ctx, dossier.Owner{Unit: task.UnitOf("t-none")}); ok || err != nil {
		t.Errorf("Get of an absent owner: %v %v", ok, err)
	}
	all, err := db.Dossiers().List(ctx)
	if err != nil || len(all) != 2 || all[0].Owner != p.Owner {
		t.Errorf("List = %+v %v", all, err)
	}
	if err := db.Dossiers().Put(ctx, dossier.Dossier{Owner: dossier.Owner{Unit: unit.Unit{Provider: "github", Key: "../x"}}}); err == nil {
		t.Error("Put accepted an owner with ..")
	}

	ro, err := OpenReadOnly(path)
	if err != nil {
		t.Fatal(err)
	}
	defer ro.Close()
	if all, err := ro.Dossiers().List(ctx); err != nil || len(all) != 2 {
		t.Errorf("read-only List = %d %v", len(all), err)
	}
	if err := ro.Dossiers().Put(ctx, p); err == nil {
		t.Error("a read-only store wrote")
	}
}

func TestDossierStoreOpenReadsItemsAndTasks(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, _ := openTemp(t)
	at := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	open := func(u unit.Unit) bool {
		t.Helper()
		ok, err := db.Dossiers().Open(ctx, dossier.Owner{Unit: u})
		if err != nil {
			t.Fatal(err)
		}
		return ok
	}
	if open(ghPull) {
		t.Fatal("a unit without items or tasks is open")
	}
	it := sampleItem(ghPull, "i1", at)
	it.State, it.Resolution = items.Resolved, items.Done
	if err := db.Items().Commit(ctx, coreitems.Commit{Items: []items.Item{it}}); err != nil {
		t.Fatal(err)
	}
	if open(ghPull) {
		t.Fatal("a resolved item keeps the unit open")
	}
	it.State, it.Resolution = items.Open, ""
	if err := db.Items().Commit(ctx, coreitems.Commit{Items: []items.Item{it}}); err != nil {
		t.Fatal(err)
	}
	if !open(ghPull) {
		t.Fatal("an open item does not keep the unit open")
	}

	e := task.Task{ID: task.NewID(), State: "next", Category: task.CategoryReady, Title: "t", Units: []unit.Unit{slackThread}, Origin: task.OriginUser, Created: at, Updated: at}
	if err := db.Tasks().Apply(ctx, []task.Change{{Op: task.OpCreate, Task: e}}); err != nil {
		t.Fatal(err)
	}
	if !open(slackThread) {
		t.Fatal("a live task does not keep the unit open")
	}
	e.State, e.Category = "done", task.CategoryDone
	if err := db.Tasks().Apply(ctx, []task.Change{{Op: task.OpUpdate, Task: e}}); err != nil {
		t.Fatal(err)
	}
	if open(slackThread) {
		t.Fatal("a done task keeps the unit open")
	}
	of := func(o dossier.Owner) bool {
		t.Helper()
		ok, err := db.Dossiers().Open(ctx, o)
		if err != nil {
			t.Fatal(err)
		}
		return ok
	}
	own := dossier.Owner{Unit: task.UnitOf(e.ID)}
	if of(own) {
		t.Fatal("a done task's own dossier is open")
	}
	e.State, e.Category = "next", task.CategoryReady
	if err := db.Tasks().Apply(ctx, []task.Change{{Op: task.OpUpdate, Task: e}}); err != nil {
		t.Fatal(err)
	}
	if !of(own) {
		t.Fatal("an open task's own dossier is not open")
	}
	if of(dossier.Owner{Unit: task.UnitOf(e.ID), Merged: "t-old"}) {
		t.Fatal("a merged dossier is open")
	}
}

func TestADossierOwnerIsTheTaskHoldingTheUnit(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, _ := openTemp(t)
	at := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	owner := func(u unit.Unit) dossier.Owner {
		t.Helper()
		o, err := db.Dossiers().Owner(ctx, u)
		if err != nil {
			t.Fatal(err)
		}
		return o
	}
	if got := owner(ghPull); got != (dossier.Owner{Unit: ghPull}) {
		t.Fatalf("a unit no task holds: %v", got)
	}
	tk := func(id task.ID, cat task.Category, created time.Time, u ...unit.Unit) task.Task {
		return task.Task{ID: id, State: task.State(cat), Category: cat, Title: string(id), Units: u, Origin: task.OriginDerived, Created: created, Updated: created}
	}
	closed := tk("t-closed", task.CategoryDone, at.Add(-2*time.Hour), ghPull)
	young := tk("t-young", task.CategoryReady, at, ghPull)
	old := tk("t-old", task.CategoryReady, at.Add(-time.Hour))
	old.Items = []task.ItemRef{{Unit: ghPull, Item: "i1"}}
	if err := db.Tasks().Apply(ctx, []task.Change{{Op: task.OpCreate, Task: closed}, {Op: task.OpCreate, Task: young}, {Op: task.OpCreate, Task: old}}); err != nil {
		t.Fatal(err)
	}
	if got, want := owner(ghPull), (dossier.Owner{Unit: task.UnitOf(old.ID)}); got != want {
		t.Fatalf("owner %v, want the oldest open task %v", got, want)
	}
	young.State, young.Category, old.State, old.Category = "done", task.CategoryDone, "done", task.CategoryDone
	if err := db.Tasks().Apply(ctx, []task.Change{{Op: task.OpUpdate, Task: young}, {Op: task.OpUpdate, Task: old}}); err != nil {
		t.Fatal(err)
	}
	if got, want := owner(ghPull), (dossier.Owner{Unit: task.UnitOf(closed.ID)}); got != want {
		t.Fatalf("owner %v, want the oldest task once none is open %v", got, want)
	}
	if got := owner(task.UnitOf("t-x")); got != (dossier.Owner{Unit: task.UnitOf("t-x")}) {
		t.Fatalf("a task's own unit: %v", got)
	}
}

func TestAgentWorktreesListsEachOnce(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, _ := openTemp(t)
	at := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	for _, a := range []AgentSession{
		{ID: "s1", Kind: "claude", Home: "/h", Worktree: "/w/a", FirstSeen: at},
		{ID: "s2", Kind: "claude", Home: "/h", Worktree: "/w/a", FirstSeen: at},
		{ID: "s3", Kind: "claude", Worktree: "/w/b", FirstSeen: at},
		{ID: "s4", Kind: "claude", FirstSeen: at},
	} {
		if err := db.SaveAgentSession(ctx, a); err != nil {
			t.Fatal(err)
		}
	}
	got, err := db.AgentWorktrees(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := []AgentWorktree{{Home: "/h", Worktree: "/w/a"}, {Worktree: "/w/b"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("AgentWorktrees = %+v, want %+v", got, want)
	}
}
