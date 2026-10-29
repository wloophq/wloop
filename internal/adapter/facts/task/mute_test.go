package facttask_test

import (
	"context"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/catalog"
	coreitems "github.com/wloophq/wloop/internal/core/items"
	coretasks "github.com/wloophq/wloop/internal/core/tasks"
	"github.com/wloophq/wloop/internal/core/tasks/taskstest"
	"github.com/wloophq/wloop/internal/core/wakes"
	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/wake"
	intentload "github.com/wloophq/wloop/internal/intent"
)

var pr7 = unit.Unit{Provider: unit.GitHub, Key: "acme/api#7"}

// muteRig is the tasks service and the wakes over e's store, both reading
// the task facts e's provider gives, as serve wires them.
func muteRig(e *env) (*coretasks.Service, *wakes.Service) {
	loaded := func() *intentload.Loaded { return &intentload.Loaded{Tasks: bundledTasks} }
	svc := coretasks.New(coretasks.Deps{Store: e.db.Tasks(), States: taskstest.BundledStates, Clock: e.clock, Wakes: e.db.Wakes(),
		Baseline: catalog.Baseline(loaded, e.p.TaskFacts)})
	w := wakes.New(wakes.Deps{Store: e.db.Wakes(), Tasks: e.db.Tasks(), Clock: e.clock, Holds: catalog.Holds(loaded, e.p.TaskFacts)})
	return svc, w
}

// awake says whether task id's wake matched: spent, or gone with the task back.
func awake(t *testing.T, e *env, id task.ID) bool {
	t.Helper()
	w, ok, err := e.db.Wakes().Of(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return !ok || w.When == ""
}

func TestAMuteUntilTheStateChangesWaitsOnEachTasksOwnOwes(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	e := newEnv(t)
	svc, w := muteRig(e)
	ana, me := items.Person{Key: "github:ana"}, items.Person{Key: "chat:ME"}
	mine := e.item(pr7, "review", items.Open, monday.Add(-time.Hour), me)
	theirs := e.item(pr7, "fix", items.Open, monday.Add(-time.Hour), ana)
	thread := e.item(chatA, "ask", items.Open, monday.Add(-time.Hour), items.Person{Key: "chat:U1"})
	e.task(task.Task{ID: "t-a", State: "next", Category: task.CategoryReady, Units: []unit.Unit{pr7}, Items: []task.ItemRef{{Unit: pr7, Item: mine.ID}}})
	e.task(task.Task{ID: "t-b", State: "waiting", Category: task.CategoryWaiting, Units: []unit.Unit{pr7}, Items: []task.ItemRef{{Unit: pr7, Item: theirs.ID}}})
	e.task(task.Task{ID: "t-c", State: "next", Category: task.CategoryReady, Units: []unit.Unit{pr7, chatA},
		Items: []task.ItemRef{{Unit: pr7, Item: mine.ID}, {Unit: chatA, Item: thread.ID}}})
	for _, id := range []task.ID{"t-a", "t-b", "t-c"} {
		if err := svc.Sleep(ctx, id, wake.Wake{When: "task.link_owes"}); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Sweep(ctx); err != nil {
		t.Fatal(err)
	}
	for _, id := range []task.ID{"t-a", "t-b", "t-c"} {
		if awake(t, e, id) {
			t.Errorf("%s woke while who it owes stayed", id)
		}
	}
	theirs.WaitingOn = []items.Person{{Key: "github:bruno"}}
	e.commit(theirs)
	if err := w.Sweep(ctx); err != nil {
		t.Fatal(err)
	}
	if !awake(t, e, "t-b") || awake(t, e, "t-a") || awake(t, e, "t-c") {
		t.Fatalf("after the fix's owner changed: a %v, b %v, c %v; want b alone", awake(t, e, "t-a"), awake(t, e, "t-b"), awake(t, e, "t-c"))
	}
	mine.WaitingOn = []items.Person{ana}
	e.commit(mine)
	if err := w.Sweep(ctx); err != nil {
		t.Fatal(err)
	}
	if !awake(t, e, "t-a") || !awake(t, e, "t-c") {
		t.Fatalf("after the review's owner changed: a %v, c %v", awake(t, e, "t-a"), awake(t, e, "t-c"))
	}
}

// commit stores it as changed.
func (e *env) commit(it items.Item) {
	e.t.Helper()
	if err := e.db.Items().Commit(context.Background(), coreitems.Commit{Items: []items.Item{it}}); err != nil {
		e.t.Fatal(err)
	}
}
