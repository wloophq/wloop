package tasks_test

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/core/change"
	"github.com/wloophq/wloop/internal/core/port"
	coretasks "github.com/wloophq/wloop/internal/core/tasks"
	"github.com/wloophq/wloop/internal/core/tasks/taskstest"
	"github.com/wloophq/wloop/internal/core/wakes/wakestest"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/wake"
	"github.com/wloophq/wloop/internal/domain/workflow"
	"github.com/wloophq/wloop/internal/meter"
	"github.com/wloophq/wloop/internal/meter/metertest"
)

// sharedRig is a setsRig whose service shares its pass while the change log, which the test notes, holds.
type sharedRig struct {
	*setsRig
	log *change.Log
}

func newSharedRig(t *testing.T) *sharedRig {
	r := &sharedRig{setsRig: newSetsRig(), log: change.NewLog(change.DefaultWindow, nil)}
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
		Changes: r.log})
	r.seed(t)
	return r
}

// screen is what one screen of the cockpit reads: a view's page, the counters and the sets of two views.
func (r *sharedRig) screen(t testing.TB) {
	t.Helper()
	r.screenIn(t, context.Background())
}

func (r *sharedRig) screenIn(t testing.TB, ctx context.Context) {
	t.Helper()
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
	r.log.Note(port.TopicTasks)
	r.screen(t)
	if r.reads != 8 || len(r.passes) != 2 {
		t.Fatalf("facts read %d times over %d passes, want 8 over 2", r.reads, len(r.passes))
	}
}

func TestANoteOfATopicTheFactsDoNotReadKeepsThePass(t *testing.T) {
	r := newSharedRig(t)
	r.screen(t)
	r.log.Note(port.TopicApp)
	r.log.Note(port.TopicJobs)
	r.screen(t)
	if r.reads != 4 || len(r.passes) != 1 {
		t.Fatalf("facts read %d times over %d passes after notes the facts do not read, want 4 in one pass", r.reads, len(r.passes))
	}
	r.log.Note(port.TopicApp | port.TopicGates)
	r.screen(t)
	if len(r.passes) != 2 {
		t.Fatalf("%d passes after a note that also moved the gates, want 2", len(r.passes))
	}
}

func TestEveryTopicButThoseTheFactsDoNotReadStartsANewPass(t *testing.T) {
	for _, name := range port.AllTopics.Names() {
		var topic port.Topic
		for i := range 64 {
			if names := port.Topic(1 << i).Names(); len(names) == 1 && names[0] == name {
				topic = port.Topic(1 << i)
			}
		}
		r := newSharedRig(t)
		r.screen(t)
		r.log.Note(topic)
		r.screen(t)
		if want := map[bool]int{true: 1, false: 2}[coretasks.PassIgnores&topic != 0]; len(r.passes) != want {
			t.Errorf("a note of %s: %d passes, want %d", name, len(r.passes), want)
		}
	}
}

func TestASharedPassMissCountsTheTopicsThatMoved(t *testing.T) {
	r := newSharedRig(t)
	ctx, sc := metertest.Start(t, context.Background())
	r.screenIn(t, ctx)
	r.log.Note(port.TopicTasks | port.TopicDrafts)
	r.log.Note(port.TopicTasks)
	r.screenIn(t, ctx)
	got := sc.Tally()
	for _, name := range []string{"tasks", "drafts"} {
		if n := got[meter.Key{Res: meter.Work, Op: "pass missed: " + name}].Calls; n != 1 {
			t.Errorf("%d misses counted for %s, want 1 (tally %v)", n, name, got)
		}
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
			r.log = change.NewLog(change.DefaultWindow, nil)
			var changes port.Changes
			if shared {
				changes = r.log
			}
			r.svc = coretasks.New(coretasks.Deps{Store: r.store, Items: r.items, Facts: r.facts, Me: me, Clock: r.clock,
				States: taskstest.BundledStates, Authority: taskstest.BundledStates.Authority, Wakes: wakestest.NewStore(),
				Views: r.lens.Views, ViewEval: eval, Changes: changes})
			for i := range 1300 {
				r.put(b, task.Task{ID: task.ID(fmt.Sprintf("t-%d", i)), State: "next", Category: task.CategoryReady, Title: "a task"})
			}
			b.ResetTimer()
			for range b.N {
				r.log.Note(port.TopicTasks)
				r.screen(b)
			}
		})
	}
}

// countedWakes are wakes that count their reads, one by one and all at once.
type countedWakes struct {
	*wakestest.Store
	ones, alls int
}

func (w *countedWakes) Of(ctx context.Context, id task.ID) (wake.Wake, bool, error) {
	w.ones++
	return w.Store.Of(ctx, id)
}

func (w *countedWakes) All(ctx context.Context) ([]wake.Wake, error) {
	w.alls++
	var out []wake.Wake
	for _, id := range []task.ID{"in-1", "nx-1", "nx-2", "wt-1"} {
		if got, ok, err := w.Store.Of(ctx, id); err != nil || ok {
			out = append(out, got)
		}
	}
	return out, nil
}

func TestTheWakesOfAPassAreReadOnce(t *testing.T) {
	r := newSetsRig()
	r.seed(t)
	ws := &countedWakes{Store: wakestest.NewStore()}
	until := t0.Add(48 * time.Hour)
	if err := ws.Put(context.Background(), wake.Wake{Task: "nx-2", Until: until}); err != nil {
		t.Fatal(err)
	}
	svc := coretasks.New(coretasks.Deps{Store: r.store, Items: r.items, Facts: r.facts, Me: me, Clock: r.clock,
		States: taskstest.BundledStates, Wakes: ws, Views: r.lens.Views, ViewEval: r.lens.Eval})
	keep := func(ctx context.Context, e task.Task) (bool, error) {
		w, ok, err := svc.WakeOf(ctx, e.ID)
		if err == nil && (ok != (e.ID == "nx-2") || ok && !w.Until.Equal(until)) {
			t.Errorf("%s: %+v %v", e.ID, w, ok)
		}
		return true, err
	}
	if _, err := svc.Sets(context.Background(), []task.View{"inbox", "ready", "waiting"}, keep); err != nil {
		t.Fatal(err)
	}
	if ws.alls != 1 || ws.ones != 0 {
		t.Fatalf("read all %d times and one by one %d times, want all once", ws.alls, ws.ones)
	}
	if _, _, err := svc.WakeOf(context.Background(), "nx-2"); err != nil || ws.ones != 1 {
		t.Fatalf("outside a pass: %v, %d reads", err, ws.ones)
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
