package sqlite_test

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	coretasks "github.com/wloophq/wloop/internal/core/tasks"
	"github.com/wloophq/wloop/internal/core/tasks/taskstest"
	"github.com/wloophq/wloop/internal/core/wakes"
	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/wake"
)

type taskClock struct{ t time.Time }

func (c *taskClock) Now() time.Time { return c.t }

type taskItems map[unit.Unit][]items.Item

func (m taskItems) Items(ctx context.Context, u unit.Unit) ([]items.Item, error) { return m[u], nil }

type taskFacts struct {
	facts map[unit.Unit]task.UnitFacts
	off   bool
}

func (f *taskFacts) UnitFacts(ctx context.Context, u unit.Unit) (task.UnitFacts, bool, error) {
	if f.off {
		return task.UnitFacts{}, false, nil
	}
	fs := f.facts[u]
	fs.Unit = u
	return fs, true, nil
}

type taskRig struct {
	svc   *coretasks.Service
	store coretasks.Store
	items taskItems
	facts *taskFacts
	clock *taskClock
	wakes *wakes.Service
}

var (
	meKey  = items.Person{Key: "github:me"}
	anaKey = items.Person{Key: "github:ana", Name: "Ana"}
)

func newTaskRig(t testing.TB) *taskRig {
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "tasks.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	store := db.Tasks()
	r := &taskRig{store: store, items: taskItems{}, facts: &taskFacts{facts: map[unit.Unit]task.UnitFacts{}}, clock: &taskClock{taskAt}}
	r.wakes = wakes.New(wakes.Deps{Store: db.Wakes(), Tasks: store, Clock: r.clock})
	r.svc = coretasks.New(coretasks.Deps{Store: store, Items: r.items, Facts: r.facts, Me: task.Me{Keys: []string{"github:me"}},
		Clock: r.clock, States: taskstest.BundledStates, Authority: taskstest.BundledStates.Authority, Wakes: db.Wakes(),
		Activity: func(ctx context.Context, id task.ID) error { return r.wakes.Event(ctx, id, wake.OnActivity, "") }})
	return r
}

func prUnit(n int) unit.Unit {
	return unit.Unit{Provider: unit.GitHub, Key: fmt.Sprintf("acme/api#%d", n)}
}

func TestServiceOverSqliteKeepsClarifiedTasksOutOfTheInboxOnActivity(t *testing.T) {
	t.Parallel()
	r := newTaskRig(t)
	ctx := context.Background()
	u := prUnit(1)
	r.items[u] = []items.Item{
		{ID: "a", Unit: u, Summary: "review", State: "open", Requester: anaKey, WaitingOn: []items.Person{meKey}, CreatedAt: taskAt},
		{ID: "b", Unit: u, Summary: "reply", State: "open", Requester: meKey, WaitingOn: []items.Person{anaKey}, CreatedAt: taskAt},
	}
	if err := r.svc.Sync(ctx, []unit.Unit{u}); err != nil {
		t.Fatal(err)
	}
	action, waiting := task.DerivedID(u, "a"), task.DerivedID(u, "b")
	r.clock.t = taskAt.Add(time.Hour)
	for _, v := range []task.Verb{task.VerbNext, task.VerbSchedule} {
		if _, err := r.svc.Do(ctx, action, coretasks.Request{Actor: task.ActorPerson, Verb: v, Args: map[string]string{"on_activity": "true"}}); err != nil {
			t.Fatal(err)
		}
	}
	r.facts.facts[u] = task.UnitFacts{LastHuman: taskAt.Add(2 * time.Hour)}
	r.clock.t = taskAt.Add(3 * time.Hour)
	if err := r.svc.Sync(ctx, []unit.Unit{u}); err != nil {
		t.Fatal(err)
	}
	if e, err := r.store.Task(ctx, action); err != nil || e.Category != task.CategoryDeferred || !e.Changed {
		t.Fatalf("the scheduled task after a new message: %+v %v, want it asleep and changed", e, err)
	}
	if _, err := r.svc.Do(ctx, action, coretasks.Request{Actor: task.ActorWorkflow, Source: "task-wake", Verb: task.VerbWake}); err != nil {
		t.Fatal(err)
	}
	for id, want := range map[task.ID]task.Category{action: task.CategoryReady, waiting: task.CategoryWaiting} {
		e, err := r.store.Task(ctx, id)
		if err != nil || e.Category != want || !e.Changed {
			t.Fatalf("%s after a new message: %+v %v, want %s and changed", id, e, err, want)
		}
	}
	r.facts.off = true
	if _, err := r.svc.Do(ctx, action, coretasks.Request{Actor: task.ActorPerson, Verb: task.VerbDone}); err == nil {
		t.Fatal("a disabled plugin's task moved")
	}
}

// BenchmarkTasksCost is what Sync and the reads cost at a realistic scale.
func BenchmarkTasksCost(b *testing.B) {
	r := newTaskRig(b)
	ctx := context.Background()
	const units = 500
	var all []unit.Unit
	for n := range units {
		u := prUnit(n)
		all = append(all, u)
		for i := range 4 {
			it := items.Item{ID: fmt.Sprint("i", i), Unit: u, Summary: "s", State: "open", Requester: anaKey,
				WaitingOn: []items.Person{meKey}, Contexts: []string{"@reply"}, CreatedAt: taskAt}
			if i >= 2 {
				it.Requester, it.WaitingOn = meKey, []items.Person{{Key: fmt.Sprint("github:p", i)}}
			}
			r.items[u] = append(r.items[u], it)
		}
	}
	if err := r.svc.Sync(ctx, all); err != nil {
		b.Fatal(err)
	}
	r.clock.t = taskAt.Add(time.Minute)
	p, err := r.store.TasksSince(ctx, task.Query{}, coretasks.Since{})
	if err != nil {
		b.Fatal(err)
	}
	for name, op := range map[string]func() error{
		"sync-all": func() error { return r.svc.Sync(ctx, all) },
		"sync-one": func() error { return r.svc.Sync(ctx, all[:1]) },
		"every-task": func() error {
			_, err := r.store.Tasks(ctx, task.Query{All: true})
			return err
		},
		"full-page": func() error {
			_, err := r.store.TasksSince(ctx, task.Query{}, coretasks.Since{})
			return err
		},
		"empty-delta": func() error {
			_, err := r.store.TasksSince(ctx, task.Query{}, coretasks.Since{Epoch: p.Epoch, Rev: p.Rev})
			return err
		},
	} {
		b.Run(name, func(b *testing.B) {
			for b.Loop() {
				if err := op(); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
