package sqlite_test

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	"github.com/wloophq/wloop/internal/core/change"
	"github.com/wloophq/wloop/internal/core/port"
	coretasks "github.com/wloophq/wloop/internal/core/tasks"
	"github.com/wloophq/wloop/internal/core/tasks/taskstest"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
)

var taskAt = time.Date(2026, 10, 20, 13, 0, 0, 0, time.UTC)

func openTasks(t *testing.T) (coretasks.Store, *change.Log) {
	t.Helper()
	log := change.NewLog(change.DefaultWindow, nil)
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "tasks.db"), log)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db.Tasks(), log
}

func applyTasks(t *testing.T, s coretasks.Store, es ...task.Task) {
	t.Helper()
	var cs []task.Change
	for _, e := range es {
		e.Become(e.State, taskstest.BundledStates)
		cs = append(cs, task.Change{Op: task.OpUpdate, Task: e})
	}
	if err := s.Apply(context.Background(), cs); err != nil {
		t.Fatal(err)
	}
}

func capture(id task.ID, s task.State, min int) task.Task {
	at := taskAt.Add(time.Duration(min) * time.Minute)
	return task.Task{ID: id, State: s, Title: string(id), Origin: task.OriginUser, Created: at, Updated: at}
}

func TestTaskStoreContract(t *testing.T) {
	t.Parallel()
	taskstest.Contract(t, func(t *testing.T) coretasks.Store { s, _ := openTasks(t); return s })
}

func TestTasksRoundTrip(t *testing.T) {
	t.Parallel()
	s, _ := openTasks(t)
	want := taskstest.Full("t-00000000000000aa")
	applyTasks(t, s, want)
	got, err := s.Task(context.Background(), want.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Same(want) || !got.Updated.Equal(want.Updated) || !slices.Equal(got.Prop("contexts").List, want.Prop("contexts").List) || got.Written["focus"] != want.Written["focus"] || !slices.Equal(got.Units, want.Units) {
		t.Fatalf("read back\n%+v\nwant\n%+v", got, want)
	}
	if got.Prop("owner").Actors[0].Aliases[0] != want.Prop("owner").Actors[0].Aliases[0] {
		t.Fatalf("owner %+v", got.Prop("owner"))
	}
}

func TestApplyNotesTasks(t *testing.T) {
	t.Parallel()
	s, log := openTasks(t)
	ctx := context.Background()
	u := unit.Unit{Provider: unit.GitHub, Key: "acme/api#7"}
	e := capture("t-1", task.State("inbox"), 0)
	e.Units = []unit.Unit{u}
	from := log.Gen()
	ev := port.Event{Unit: u, Kind: "task_test", Key: "k", At: taskAt}
	if err := s.Apply(ctx, []task.Change{{Op: task.OpCreate, Task: e}}, ev); err != nil {
		t.Fatal(err)
	}
	got := log.Since(from, ^port.Topic(0))
	if log.Gen()-from != 1 || !got.Has(port.TopicTasks) || !slices.Equal(got.Units, []unit.Unit{u, task.UnitOf(e.ID)}) {
		t.Fatalf("%d notes: %+v", log.Gen()-from, got)
	}
	from = log.Gen()
	bad := []task.Change{{Op: task.OpCreate, Task: capture("t-2", task.State("inbox"), 1)}, {Op: task.OpCreate, Task: task.Task{}}}
	if err := s.Apply(ctx, bad); err == nil {
		t.Fatal("a task without an id was written")
	}
	if _, err := s.Task(ctx, "t-2"); !errors.Is(err, coretasks.ErrNoTask) || log.Gen() != from {
		t.Fatalf("a failed apply wrote or noted: %v, %d notes", err, log.Gen()-from)
	}
}

func TestEmptyApplyNotesNothing(t *testing.T) {
	t.Parallel()
	s, log := openTasks(t)
	ctx := context.Background()
	e := capture("t-1", task.State("inbox"), 0)
	applyTasks(t, s, e)
	before, err := s.TasksSince(ctx, task.Query{}, coretasks.Since{})
	if err != nil {
		t.Fatal(err)
	}
	from := log.Gen()
	e.Updated = e.Updated.Add(time.Hour)
	applyTasks(t, s, e)
	applyTasks(t, s)
	after, err := s.TasksSince(ctx, task.Query{}, coretasks.Since{})
	if err != nil {
		t.Fatal(err)
	}
	if log.Gen() != from || after.Rev != before.Rev || after.Epoch != before.Epoch {
		t.Fatalf("%d notes, rev %d -> %d", log.Gen()-from, before.Rev, after.Rev)
	}
}

func TestTasksSinceStore(t *testing.T) {
	t.Parallel()
	s, _ := openTasks(t)
	ctx := context.Background()
	a, b, c := capture("t-a", task.State("next"), 0), capture("t-b", task.State("next"), 1), capture("t-c", task.State("done"), 2)
	applyTasks(t, s, a, b, c)
	q := task.Query{}
	first, err := s.TasksSince(ctx, q, coretasks.Since{})
	if err != nil || !first.Full || !slices.Equal(taskIDs(first.Tasks), []task.ID{"t-a", "t-b"}) {
		t.Fatalf("first page %v %v", taskIDs(first.Tasks), err)
	}
	a.Title, b.State, b.Category = "a2", task.State("done"), task.CategoryDone
	applyTasks(t, s, a, b)
	page, err := s.TasksSince(ctx, q, coretasks.Since{Epoch: first.Epoch, Rev: first.Rev})
	if err != nil {
		t.Fatal(err)
	}
	if page.Full || !slices.Equal(taskIDs(page.Tasks), []task.ID{"t-a", "t-b"}) || len(page.Removed) != 0 {
		t.Fatalf("page tasks %v removed %v full %v", taskIDs(page.Tasks), page.Removed, page.Full)
	}
	other, err := s.TasksSince(ctx, task.Query{All: true}, coretasks.Since{Epoch: "another", Rev: page.Rev})
	if err != nil || !other.Full || len(other.Tasks) != 3 {
		t.Fatalf("another epoch %+v %v", other, err)
	}
	ahead, err := s.TasksSince(ctx, q, coretasks.Since{Epoch: page.Epoch, Rev: page.Rev + 5})
	if err != nil || !ahead.Full {
		t.Fatalf("a position ahead %+v %v", ahead, err)
	}
}

func taskIDs(es []task.Task) []task.ID {
	out := []task.ID{}
	for _, e := range es {
		out = append(out, e.ID)
	}
	return out
}

func TestVerbEventsWriteWithOrWithoutAChange(t *testing.T) {
	t.Parallel()
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "tasks.db"), change.NewLog(change.DefaultWindow, nil))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	ctx := context.Background()
	e := capture("t-1", task.State("next"), 0)
	applyTasks(t, db.Tasks(), e)
	done := e
	done.State, done.Category = "done", task.CategoryDone
	ev := port.Event{Unit: task.UnitOf(e.ID), Kind: task.VerbEventKind(task.VerbDone), Data: "next", At: taskAt}
	if err := db.Tasks().Apply(ctx, []task.Change{{Op: task.OpUpdate, Task: done}}, ev); err != nil {
		t.Fatal(err)
	}
	if err := db.Tasks().Apply(ctx, nil, ev); err != nil {
		t.Fatal(err)
	}
	got, err := db.Events().Since(ctx, 0, task.VerbEventKinds([]string{"done"}))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Unit != task.UnitOf(e.ID) || got[0].Data != "next" || got[1].Kind != "verb_done" {
		t.Fatalf("events %+v", got)
	}
}
