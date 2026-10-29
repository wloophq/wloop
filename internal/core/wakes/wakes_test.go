package wakes_test

import (
	"context"
	"slices"
	"testing"
	"time"

	coretasks "github.com/wloophq/wloop/internal/core/tasks"
	"github.com/wloophq/wloop/internal/core/tasks/taskstest"
	"github.com/wloophq/wloop/internal/core/wakes"
	"github.com/wloophq/wloop/internal/core/wakes/wakestest"
	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/wake"
)

var (
	t0 = time.Date(2026, 10, 28, 9, 0, 0, 0, time.UTC)
	pr = unit.Unit{Provider: unit.GitHub, Key: "acme/api#7"}
)

type clock struct{ at *time.Time }

func (c clock) Now() time.Time { return *c.at }

// unitFacts are the facts of every unit: a person's activity at LastHuman.
type unitFacts struct{ last *time.Time }

func (f unitFacts) UnitFacts(_ context.Context, u unit.Unit) (task.UnitFacts, bool, error) {
	return task.UnitFacts{Unit: u, LastHuman: *f.last}, true, nil
}

type noItems struct{}

func (noItems) Items(context.Context, unit.Unit) ([]items.Item, error) { return nil, nil }

// fixture is the tasks service and the wakes over one in-memory store, a
// task wake.OnActivity tells the wakes of.
type fixture struct {
	now, last time.Time
	tasks     *taskstest.Store
	store     *wakestest.Store
	svc       *coretasks.Service
	wakes     *wakes.Service
	holds     bool
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{now: t0, tasks: taskstest.New(), store: wakestest.NewStore()}
	f.wakes = wakes.New(wakes.Deps{Store: f.store, Tasks: f.tasks, Clock: clock{&f.now},
		Holds: func(context.Context, wake.Wake, task.Task) (bool, error) { return f.holds, nil }})
	f.svc = coretasks.New(coretasks.Deps{Store: f.tasks, Items: noItems{}, Facts: unitFacts{&f.last}, Clock: clock{&f.now},
		States: taskstest.BundledStates, Authority: taskstest.BundledStates.Authority, Wakes: f.store,
		Activity: func(ctx context.Context, id task.ID) error { return f.wakes.Event(ctx, id, wake.OnActivity, "") }})
	e := task.Task{ID: "t-01", State: "next", Category: task.CategoryReady, Units: []unit.Unit{pr}, Origin: task.OriginUser,
		Created: t0.Add(-time.Hour), Updated: t0.Add(-time.Hour)}
	if err := f.tasks.Apply(context.Background(), []task.Change{{Op: task.OpCreate, Task: e}}); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *fixture) do(t *testing.T, v task.Verb, a task.Actor, args map[string]string) task.Task {
	t.Helper()
	res, err := f.svc.Do(context.Background(), "t-01", coretasks.Request{Verb: v, Args: args, Actor: a, Source: "test"})
	if err != nil {
		t.Fatalf("%s: %v", v, err)
	}
	return res.Task
}

// woke are the task.woke events recorded, with their data.
func (f *fixture) woke() []string {
	var out []string
	for _, ev := range f.tasks.Events {
		if ev.Kind == wake.EventWoke {
			out = append(out, ev.Unit.Key+" "+ev.Data)
		}
	}
	return out
}

func TestAWakeOnActivityMovesTheTaskBack(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	if e := f.do(t, task.VerbSchedule, task.ActorPerson, map[string]string{"on_activity": "true"}); e.State != "scheduled" || !e.Snoozed() {
		t.Fatalf("scheduled %+v", e)
	}
	f.now = t0.Add(time.Hour)
	if err := f.svc.Sync(ctx, []unit.Unit{pr}); err != nil {
		t.Fatal(err)
	}
	if got := f.woke(); len(got) != 0 {
		t.Fatalf("woke with no activity since: %v", got)
	}
	f.last = t0.Add(2 * time.Hour)
	f.now = t0.Add(3 * time.Hour)
	if err := f.svc.Sync(ctx, []unit.Unit{pr}); err != nil {
		t.Fatal(err)
	}
	if got := f.woke(); !slices.Equal(got, []string{"t-01 next"}) {
		t.Fatalf("woke %v, want once, back to next", got)
	}
	if err := f.svc.Sync(ctx, []unit.Unit{pr}); err != nil {
		t.Fatal(err)
	}
	if got := f.woke(); len(got) != 1 {
		t.Fatalf("a spent wake woke again: %v", got)
	}
	e := f.do(t, task.VerbWake, task.ActorWorkflow, nil)
	if e.State != "next" || e.Category != task.CategoryReady || e.Asleep {
		t.Fatalf("woken task %+v, want it back in next", e)
	}
	if _, ok, _ := f.store.Of(ctx, "t-01"); ok {
		t.Fatal("the woken task kept its wake")
	}
}

func TestAWakeOnAConditionWaitsForTheSituationToChange(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	if err := f.svc.Sleep(ctx, "t-01", wake.Wake{When: `size(task.link_owes) != 1 || !("me" in task.link_owes)`}); err != nil {
		t.Fatal(err)
	}
	e, _ := f.tasks.Task(ctx, "t-01")
	if e.Category != task.CategoryDeferred || !e.Asleep {
		t.Fatalf("a task under a wake: %+v", e)
	}
	if err := f.wakes.Sweep(ctx); err != nil {
		t.Fatal(err)
	}
	if got := f.woke(); len(got) != 0 {
		t.Fatalf("woke while the situation stays: %v", got)
	}
	f.holds = true
	if err := f.wakes.Sweep(ctx); err != nil {
		t.Fatal(err)
	}
	if err := f.wakes.Sweep(ctx); err != nil {
		t.Fatal(err)
	}
	if got := f.woke(); !slices.Equal(got, []string{"t-01 next"}) {
		t.Fatalf("woke %v, want once, back to next", got)
	}
	if w, _, _ := f.store.Of(ctx, "t-01"); w.When != "" || w.Return != "next" {
		t.Fatalf("the spent wake %+v", w)
	}
}

func TestAWakeIsDueAtItsTimeAndExpiresUnmatched(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	if err := f.svc.Sleep(ctx, "t-01", wake.Wake{Until: t0.Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	f.now = t0.Add(time.Hour - time.Nanosecond)
	if err := f.wakes.Sweep(ctx); err != nil || len(f.woke()) != 0 {
		t.Fatalf("woke early: %v %v", f.woke(), err)
	}
	if next, err := f.wakes.Next(ctx); err != nil || !next.Equal(t0.Add(time.Hour)) {
		t.Fatalf("next %v %v", next, err)
	}
	f.now = t0.Add(time.Hour)
	if err := f.wakes.Sweep(ctx); err != nil || len(f.woke()) != 1 {
		t.Fatalf("not woken at its time: %v %v", f.woke(), err)
	}
	if err := f.store.Put(ctx, wake.Wake{Task: "t-01", When: "true", Expires: t0.Add(2 * time.Hour), Return: "next"}); err != nil {
		t.Fatal(err)
	}
	f.now = t0.Add(2 * time.Hour)
	if err := f.wakes.Sweep(ctx); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := f.store.Of(ctx, "t-01"); ok || len(f.woke()) != 1 {
		t.Fatalf("an expired wake: kept %v, woke %v", ok, f.woke())
	}
}
