package tasks_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/core/gates"
	"github.com/wloophq/wloop/internal/core/gates/gatestest"
	coretasks "github.com/wloophq/wloop/internal/core/tasks"
	"github.com/wloophq/wloop/internal/core/tasks/taskstest"
	"github.com/wloophq/wloop/internal/domain/gate"
	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
)

func heldRig(t *testing.T) (*rig, *gatestest.Store) {
	t.Helper()
	r := newRig()
	gs := gatestest.NewStore(func() time.Time { return t0 })
	gs.Holding = []gate.Kind{gate.KindQuestion}
	r.svc = coretasks.New(coretasks.Deps{Store: r.store, Items: r.items, Facts: r.facts, Me: me, Clock: r.clock, States: taskstest.BundledStates,
		Authority: taskstest.BundledStates.Authority, Wakes: r.wakes, Gates: gatestest.TaskGates{Store: gs}})
	return r, gs
}

func TestAnOpenQuestionKeepsItsTaskOpenUntilAPersonSettlesIt(t *testing.T) {
	ctx := context.Background()
	r, gs := heldRig(t)
	r.items.byUnit[unitU] = []items.Item{owed(unitU, "a")}
	if err := r.svc.Sync(ctx, []unit.Unit{unitU}); err != nil {
		t.Fatal(err)
	}
	es, _ := r.store.TasksOf(ctx, unitU)
	if len(es) != 1 {
		t.Fatalf("%d tasks, want the owed item's", len(es))
	}
	question, err := gs.Open(ctx, gate.Gate{Task: es[0].ID, Unit: unitU, Kind: gate.KindQuestion})
	if err != nil {
		t.Fatal(err)
	}
	r.items.byUnit[unitU][0].State = "resolved"
	if err := r.svc.Sync(ctx, []unit.Unit{unitU}); err != nil {
		t.Fatal(err)
	}
	if got := r.task(t, es[0].ID); !got.Open() {
		t.Fatalf("the task is %s with its question open, want it open: the question still asks the person", got.Category)
	}
	if _, err := gs.Resolve(ctx, question.ID, gates.Resolution{Status: gate.Dismissed, Intent: "dismiss", By: task.ActorPerson}); err != nil {
		t.Fatal(err)
	}
	if err := r.svc.Sync(ctx, []unit.Unit{unitU}); err != nil {
		t.Fatal(err)
	}
	if got := r.task(t, es[0].ID); got.Open() {
		t.Fatalf("the task is %s once its question closed, want it ended", got.Category)
	}
}

func TestOnlyAPersonEndsATaskAnOpenQuestionHolds(t *testing.T) {
	ctx := context.Background()
	r, gs := heldRig(t)
	e := r.put(t, task.Task{ID: "t-asked", State: "next", Origin: task.OriginUser})
	if _, err := gs.Open(ctx, gate.Gate{Task: e.ID, Kind: gate.KindQuestion}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.svc.Do(ctx, e.ID, coretasks.Request{Actor: task.ActorWorkflow, Verb: task.VerbDone}); !errors.Is(err, coretasks.ErrVerb) {
		t.Fatalf("a workflow's done on a held task: %v, want ErrVerb", err)
	}
	if _, err := r.svc.Do(ctx, e.ID, coretasks.Request{Actor: task.ActorWorkflow, Verb: task.VerbWaiting}); err != nil {
		t.Fatalf("a workflow's waiting on a held task: %v, want it run: it ends nothing", err)
	}
	if _, err := r.svc.Do(ctx, e.ID, coretasks.Request{Actor: task.ActorPerson, Verb: task.VerbDone}); err != nil {
		t.Fatalf("a person's done on a held task: %v", err)
	}
	if got := r.task(t, e.ID); got.Open() {
		t.Fatalf("the task is %s after the person's done, want it ended", got.Category)
	}
}

func TestAUnitThatClosedEndsATaskAnOpenQuestionHolds(t *testing.T) {
	ctx := context.Background()
	r, gs := heldRig(t)
	e := r.put(t, task.Task{ID: "t-asked", State: "next", Origin: task.OriginUser, Units: []unit.Unit{unitU}})
	if _, err := gs.Open(ctx, gate.Gate{Task: e.ID, Unit: unitU, Kind: gate.KindQuestion}); err != nil {
		t.Fatal(err)
	}
	if err := r.svc.UnitClosed(ctx, unitU); err != nil {
		t.Fatal(err)
	}
	if got := r.task(t, e.ID); got.Open() {
		t.Fatalf("the task is %s after its unit closed, want it ended: the end of the unit makes the question moot", got.Category)
	}
}
