package tasks_test

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/core/port"
	coretasks "github.com/wloophq/wloop/internal/core/tasks"
	"github.com/wloophq/wloop/internal/core/tasks/taskstest"
	"github.com/wloophq/wloop/internal/core/wakes/wakestest"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/workflow"
)

// sharedRig is a setsRig whose service shares its pass while the store's generation, which the test moves,
// holds.
type sharedRig struct {
	*setsRig
	gen atomic.Uint64
}

func newSharedRig(t *testing.T) *sharedRig {
	r := &sharedRig{setsRig: newSetsRig()}
	eval := func(ctx context.Context, view string, e task.Task) (bool, error) {
		pass, ok := coretasks.PassOf(ctx)
		if !ok {
			return false, errors.New("no pass")
		}
		if !slices.Contains(r.passes, pass) {
			r.passes = append(r.passes, pass)
		}
		if _, err := pass.Facts(e.ID, func() (workflow.Facts, error) { r.reads++; return workflow.Facts{}, nil }); err != nil {
			return false, err
		}
		return r.lens.Eval(ctx, view, e)
	}
	r.svc = coretasks.New(coretasks.Deps{Store: r.store, Items: r.items, Facts: r.facts, Me: me, Clock: r.clock, States: taskstest.BundledStates,
		Authority: taskstest.BundledStates.Authority, Wakes: wakestest.NewStore(), Views: r.lens.Views, ViewEval: eval,
		Gen: func() port.Generation { return port.Generation(r.gen.Load()) }})
	r.seed(t)
	return r
}

// screen is what one screen of the cockpit reads: a view's page, the counters and the sets of two views.
func (r *sharedRig) screen(t testing.TB) {
	t.Helper()
	ctx := context.Background()
	if _, err := r.svc.TasksSince(ctx, task.Query{View: "ready"}, coretasks.Since{}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := r.svc.CountsWhere(ctx, "", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := r.svc.Sets(ctx, []task.View{"inbox", "waiting"}, nil); err != nil {
		t.Fatal(err)
	}
}

func TestTheReadsOfOneGenerationShareOnePass(t *testing.T) {
	r := newSharedRig(t)
	r.screen(t)
	r.screen(t)
	if r.reads != 4 || len(r.passes) != 1 {
		t.Fatalf("facts read %d times over %d passes, want each task's once (4) in one pass", r.reads, len(r.passes))
	}
}

func TestAWriteToTheStoreStartsANewPass(t *testing.T) {
	r := newSharedRig(t)
	r.screen(t)
	r.gen.Add(1)
	r.screen(t)
	if r.reads != 8 || len(r.passes) != 2 {
		t.Fatalf("facts read %d times over %d passes, want 8 over 2", r.reads, len(r.passes))
	}
}

func TestAPassIsNotSharedPastItsReuse(t *testing.T) {
	r := newSharedRig(t)
	r.screen(t)
	r.clock.t = r.clock.t.Add(coretasks.PassReuse - time.Nanosecond)
	r.screen(t)
	if len(r.passes) != 1 {
		t.Fatalf("%d passes within the reuse, want 1", len(r.passes))
	}
	r.clock.t = r.clock.t.Add(time.Nanosecond)
	r.screen(t)
	if len(r.passes) != 2 {
		t.Fatalf("%d passes once the reuse ran out, want 2", len(r.passes))
	}
}

func TestAPageNewerThanTheSharedPassListsFromTheStore(t *testing.T) {
	r := newSharedRig(t)
	r.screen(t)
	// Written but not yet noted: the generation has not moved.
	r.put(t, task.Task{ID: "nx-3", State: "next", Category: task.CategoryReady, Title: "book the room", Created: t0.Add(2 * time.Hour), Updated: t0.Add(2 * time.Hour)})
	p, err := r.svc.TasksSince(context.Background(), task.Query{View: "ready"}, coretasks.Since{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(ids(p.Tasks), "nx-3") || p.Counters.Views["ready"] != 3 {
		t.Fatalf("page %v, counters %v: want the task the page's position holds", ids(p.Tasks), p.Counters.Views)
	}
}

// BenchmarkScreen reads one screen of the cockpit after a write, over 1,300 tasks whose facts each cost tens of
// microseconds as the store's do, with the pass shared and without.
func BenchmarkScreen(b *testing.B) {
	for _, shared := range []bool{false, true} {
		b.Run(map[bool]string{false: "own-pass", true: "shared-pass"}[shared], func(b *testing.B) {
			r := &sharedRig{setsRig: newSetsRig()}
			eval := func(ctx context.Context, view string, e task.Task) (bool, error) {
				pass, _ := coretasks.PassOf(ctx)
				if _, err := pass.Facts(e.ID, func() (workflow.Facts, error) {
					sum := sha256.Sum256([]byte(e.ID))
					for range 400 {
						sum = sha256.Sum256(sum[:])
					}
					return workflow.Facts{"sum": sum}, nil
				}); err != nil {
					return false, err
				}
				return r.lens.Eval(ctx, view, e)
			}
			var gen func() port.Generation
			if shared {
				gen = func() port.Generation { return port.Generation(r.gen.Load()) }
			}
			r.svc = coretasks.New(coretasks.Deps{Store: r.store, Items: r.items, Facts: r.facts, Me: me, Clock: r.clock,
				States: taskstest.BundledStates, Authority: taskstest.BundledStates.Authority, Wakes: wakestest.NewStore(),
				Views: r.lens.Views, ViewEval: eval, Gen: gen})
			for i := range 1300 {
				r.put(b, task.Task{ID: task.ID(fmt.Sprintf("t-%d", i)), State: "next", Category: task.CategoryReady, Title: "a task"})
			}
			b.ResetTimer()
			for range b.N {
				r.gen.Add(1)
				r.screen(b)
			}
		})
	}
}

func TestWithoutAGenerationEveryReadHasItsOwnPass(t *testing.T) {
	r := newSetsRig()
	r.seed(t)
	ctx := context.Background()
	for range 2 {
		if _, err := r.svc.Sets(ctx, []task.View{"inbox"}, nil); err != nil {
			t.Fatal(err)
		}
	}
	if len(r.passes) != 2 {
		t.Fatalf("%d passes, want 2", len(r.passes))
	}
}
