package tasks_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	coretasks "github.com/wloophq/wloop/internal/core/tasks"
	"github.com/wloophq/wloop/internal/core/tasks/taskstest"
	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
)

func TestServicePropContract(t *testing.T) {
	taskstest.PropContract(t, func(t *testing.T) (coretasks.PropStore, func(task.ID)) {
		r := newRig()
		return r.svc, func(id task.ID) {
			r.put(t, task.Task{ID: id, State: "inbox", Title: "Call Ana", Origin: task.OriginUser})
		}
	})
}

// TestATaskSetDuringAVerbIsKept stops a verb between reading its task and writing it, and sends a task.set
// meanwhile: the verb goes on once the set waits for the service's lock, or once the set returned, which only
// a set that skips the lock does, and so writes its older copy over the set.
func TestATaskSetDuringAVerbIsKept(t *testing.T) {
	t.Parallel()
	r := newRig()
	r.put(t, task.Task{ID: "t-1", State: "inbox", Title: "a", Origin: task.OriginUser})
	svc := coretasks.New(coretasks.Deps{Store: r.store, Items: r.items, Facts: r.facts, Me: me, Clock: r.clock, States: taskstest.BundledStates})
	read, waiting, setDone := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var once, waited sync.Once
	coretasks.SetRaceHooks(svc, func() {
		once.Do(func() {
			close(read)
			select {
			case <-waiting:
			case <-setDone:
			}
		})
	}, func() { waited.Do(func() { close(waiting) }) })
	ctx := context.Background()
	var wg sync.WaitGroup
	t.Cleanup(wg.Wait)
	var verbErr, setErr error
	wg.Go(func() {
		_, verbErr = svc.Do(ctx, "t-1", coretasks.Request{Actor: task.ActorPerson, Verb: task.VerbNext})
	})
	<-read
	wg.Go(func() {
		defer close(setDone)
		setErr = svc.SetProp(ctx, "t-1", "priority", task.Value{Text: "high"}, task.WriterWorkflow)
	})
	wg.Wait()
	if verbErr != nil || setErr != nil {
		t.Fatalf("verb %v, set %v", verbErr, setErr)
	}
	got := r.task(t, "t-1")
	if got.Category != task.CategoryReady || got.Prop("priority").Text != "high" || got.Written["priority"].By != task.WriterWorkflow {
		t.Fatalf("after both: %+v", got)
	}
}

// withoutCategory are the bundled states but those of one category.
type withoutCategory struct {
	coretasks.States
	gone task.Category
}

func (w withoutCategory) FirstOf(c task.Category) string {
	if c == w.gone {
		return ""
	}
	return w.States.FirstOf(c)
}

func TestASyncIntoACategoryWithNoStateFailsAndStoresNothing(t *testing.T) {
	r := newRig()
	r.svc = coretasks.New(coretasks.Deps{Store: r.store, Items: r.items, Facts: r.facts, Me: me, Clock: r.clock,
		States: withoutCategory{taskstest.BundledStates, task.CategoryInbox}})
	r.items.byUnit[unitU] = []items.Item{owed(unitU, "a")}
	if err := r.svc.Sync(context.Background(), []unit.Unit{unitU}); !errors.Is(err, task.ErrNoStateOf) {
		t.Fatalf("sync %v, want ErrNoStateOf", err)
	}
	if es, err := r.store.TasksOf(context.Background(), unitU); err != nil || len(es) != 0 {
		t.Fatalf("stored %+v %v", es, err)
	}
}
