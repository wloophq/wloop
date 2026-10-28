package tasks_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/wloophq/wloop/internal/core/port"
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
			r.put(t, task.Task{ID: id, Kind: task.KindCapture, State: "inbox", Title: "Call Ana", Origin: task.OriginUser})
		}
	})
}

// gatedStore holds the first Apply until open is closed, telling held when it is waiting.
type gatedStore struct {
	coretasks.Store
	once       sync.Once
	held, open chan struct{}
}

func (g *gatedStore) Apply(ctx context.Context, cs []task.Change, evs ...port.Event) error {
	g.once.Do(func() {
		close(g.held)
		<-g.open
	})
	return g.Store.Apply(ctx, cs, evs...)
}

// TestATaskSetDuringAVerbIsKept sends a task.set while a verb holds its write: the set waits for the verb's
// write and lands on the task it wrote, so neither undoes the other.
func TestATaskSetDuringAVerbIsKept(t *testing.T) {
	t.Parallel()
	r := newRig()
	r.put(t, task.Task{ID: "t-1", Kind: task.KindCapture, State: "inbox", Title: "a", Origin: task.OriginUser})
	g := &gatedStore{Store: r.store, held: make(chan struct{}), open: make(chan struct{})}
	svc := coretasks.New(coretasks.Deps{Store: g, Items: r.items, Facts: r.facts, Me: me, Clock: r.clock, States: taskstest.BundledStates})
	ctx := context.Background()
	var wg sync.WaitGroup
	t.Cleanup(wg.Wait)
	var verbErr, setErr error
	wg.Go(func() { _, verbErr = svc.Do(ctx, "t-1", coretasks.Request{Verb: task.VerbNext}) })
	<-g.held
	wg.Go(func() { setErr = svc.SetProp(ctx, "t-1", "priority", task.Value{Text: "high"}, task.WriterWorkflow) })
	close(g.open)
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
