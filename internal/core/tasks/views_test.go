package tasks_test

import (
	"context"
	"slices"
	"testing"
	"testing/synctest"
	"time"

	coretasks "github.com/wloophq/wloop/internal/core/tasks"
	"github.com/wloophq/wloop/internal/core/tasks/taskstest"
	"github.com/wloophq/wloop/internal/domain/task"
)

func pageIDs(es []task.Task) []task.ID {
	out := []task.ID{}
	for _, e := range es {
		out = append(out, e.ID)
	}
	return out
}

// TestTasksSincePagesAView checks that a paged view's page since a position lists the tasks written after it
// that are in the view and removes those written that are not, and that a view reading more than a task's own
// fields is served whole.
func TestTasksSincePagesAView(t *testing.T) {
	ctx := context.Background()
	r := newRig()
	r.lens.add(testView{coretasks.ViewSpec{ID: "owed", Over: task.OverTasks}, func(e task.Task) bool { return e.Category == task.CategoryReady }})
	r.put(t, task.Task{ID: "a", State: "inbox", Category: task.CategoryInbox})
	r.put(t, task.Task{ID: "b", State: "inbox", Category: task.CategoryInbox, Created: t0.Add(time.Second), Updated: t0})
	r.put(t, task.Task{ID: "c", State: "next", Category: task.CategoryReady})
	first, err := r.svc.TasksSince(ctx, task.Query{View: "inbox"}, coretasks.Since{})
	if err != nil || !first.Full || !slices.Equal(pageIDs(first.Tasks), []task.ID{"a", "b"}) || first.Counters.Views["inbox"] != 2 {
		t.Fatalf("first page %+v %v", first, err)
	}
	owed, err := r.svc.TasksSince(ctx, task.Query{View: "owed"}, coretasks.Since{})
	if err != nil || !slices.Equal(pageIDs(owed.Tasks), []task.ID{"c"}) {
		t.Fatalf("owed %+v %v", owed, err)
	}
	if _, err := r.svc.Do(ctx, "b", coretasks.Request{Actor: task.ActorPerson, Verb: task.VerbNext}); err != nil {
		t.Fatal(err)
	}
	a := r.task(t, "a")
	a.Title, a.Updated = "a2", t0.Add(time.Minute)
	if err := r.store.Apply(ctx, []task.Change{{Op: task.OpUpdate, Task: a}}); err != nil {
		t.Fatal(err)
	}
	page, err := r.svc.TasksSince(ctx, task.Query{View: "inbox"}, coretasks.Since{Epoch: first.Epoch, Rev: first.Rev})
	if err != nil || page.Full || !slices.Equal(pageIDs(page.Tasks), []task.ID{"a"}) || page.Tasks[0].Title != "a2" ||
		!slices.Equal(page.Removed, []task.ID{"b"}) || page.Rev <= first.Rev || page.Counters.Views["inbox"] != 1 {
		t.Fatalf("inbox since %+v %v", page, err)
	}
	again, err := r.svc.TasksSince(ctx, task.Query{View: "owed"}, coretasks.Since{Epoch: owed.Epoch, Rev: owed.Rev})
	if err != nil || !again.Full || !slices.Equal(pageIDs(again.Tasks), []task.ID{"c", "b"}) {
		t.Fatalf("a view served whole: %+v %v", again, err)
	}
	if p, err := r.svc.TasksSince(ctx, task.Query{View: "inbox"}, coretasks.Since{Epoch: "other", Rev: page.Rev}); err != nil || !p.Full || !slices.Equal(pageIDs(p.Tasks), []task.ID{"a"}) {
		t.Fatalf("another epoch %+v %v", p, err)
	}
}

// TestATimeVaryingViewRepagesWhenTheClockMovesATask checks that a view whose predicate reads the time, served
// Full, lists a task the clock moved into it with no write.
func TestATimeVaryingViewRepagesWhenTheClockMovesATask(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx := context.Background()
		store := taskstest.New()
		lens := newTestLens()
		start := time.Now()
		lens.add(testView{coretasks.ViewSpec{ID: "overdue", Over: task.OverTasks},
			func(e task.Task) bool { return e.Open() && time.Now().After(e.Created.Add(time.Hour)) }})
		svc := coretasks.New(coretasks.Deps{Store: store, States: taskstest.BundledStates, Clock: realClock{}, Views: lens.Views, ViewEval: lens.Eval})
		e := task.Task{ID: "t-late", State: "next", Category: task.CategoryReady, Origin: task.OriginUser, Created: start, Updated: start}
		if err := store.Apply(ctx, []task.Change{{Op: task.OpCreate, Task: e}}); err != nil {
			t.Fatal(err)
		}
		first, err := svc.TasksSince(ctx, task.Query{View: "overdue"}, coretasks.Since{})
		if err != nil || len(first.Tasks) != 0 {
			t.Fatalf("first page %+v %v", first, err)
		}
		time.Sleep(time.Hour + time.Second)
		synctest.Wait()
		page, err := svc.TasksSince(ctx, task.Query{View: "overdue"}, coretasks.Since{Epoch: first.Epoch, Rev: first.Rev})
		if err != nil || page.Rev != first.Rev || !page.Full || !slices.Equal(pageIDs(page.Tasks), []task.ID{"t-late"}) || page.Counters.Views["overdue"] != 1 {
			t.Fatalf("after an hour with no write: %+v %v", page, err)
		}
	})
}

type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }

// TestSequentialProjectShowsItsFirstAction checks that a view's sort runs over the base order, which keeps a
// parent's children in sibling order wherever their creation puts them.
func TestSequentialProjectShowsItsFirstAction(t *testing.T) {
	ctx := context.Background()
	r := newProjectRig()
	r.project(t, "p", func(e *task.Task) { e.Sequential = true })
	for i, a := range []struct {
		id  task.ID
		pos int
	}{{"a3", 3}, {"a1", 1}, {"a2", 2}} {
		at := t0.Add(time.Duration(i+1) * time.Minute)
		r.put(t, task.Task{ID: a.id, State: "next", Category: task.CategoryReady, Parent: "p", Position: a.pos, Origin: task.OriginUser, Created: at, Updated: at})
	}
	r.put(t, task.Task{ID: "x", State: "next", Category: task.CategoryReady, Created: t0.Add(90 * time.Second), Updated: t0})
	first := func(ts []task.Task) task.ID {
		for _, e := range ts {
			if e.Parent == "p" {
				return e.ID
			}
		}
		return ""
	}
	sorted := coretasks.New(coretasks.Deps{Store: r.store, States: taskstest.BundledStates, Clock: r.clock, Views: r.lens.Views, ViewEval: r.lens.Eval,
		ViewSort: func(_ context.Context, _ string, ts []task.Task) error {
			slices.SortStableFunc(ts, func(a, b task.Task) int {
				return btoi(first(ts) == b.ID) - btoi(first(ts) == a.ID)
			})
			return nil
		}})
	got, err := sorted.Tasks(ctx, task.Query{View: "ready"})
	if err != nil || !slices.Equal(pageIDs(got), []task.ID{"a1", "p", "x", "a2", "a3"}) {
		t.Fatalf("ready %v %v", pageIDs(got), err)
	}
	r.do(t, "a1", task.VerbDone, nil)
	if got, err = sorted.Tasks(ctx, task.Query{View: "ready"}); err != nil || pageIDs(got)[0] != "a2" {
		t.Fatalf("after a1 is done: %v %v", pageIDs(got), err)
	}
}

func btoi(b bool) int {
	if b {
		return 1
	}
	return 0
}

// TestAKindsOwnDismissAndDoneBindByHandler checks that a kind's handler binds by the handler the profile gives
// a verb, never by the verb's name: a draft's drop runs on dismiss, and one registered under the name
// "dismiss" runs on nothing.
func TestAKindsOwnDismissAndDoneBindByHandler(t *testing.T) {
	ctx := context.Background()
	r := newRig()
	var ran []string
	run := func(name string) coretasks.VerbHandler {
		return func(_ context.Context, e task.Task, req coretasks.Request) (coretasks.Result, error) {
			ran = append(ran, name+" "+string(req.Verb))
			return coretasks.Result{Task: e}, nil
		}
	}
	r.svc.Register(task.OriginDraft, coretasks.HandlerDrop, run("drop"))
	r.svc.Register(task.OriginDraft, coretasks.HandlerFinish, run("finish"))
	r.svc.Register(task.OriginDraft, "dismiss", run("by name"))
	r.svc.RegisterView(task.OverGates, "draft:", outbox{drafts: []task.Task{{ID: "draft:1", Origin: task.OriginDraft}}})
	for _, v := range []task.Verb{task.VerbDismiss, task.VerbDone} {
		if _, err := r.svc.Do(ctx, "draft:1", coretasks.Request{Actor: task.ActorPerson, Verb: v}); err != nil {
			t.Fatalf("%s: %v", v, err)
		}
	}
	if want := []string{"drop dismiss", "finish done"}; !slices.Equal(ran, want) {
		t.Fatalf("ran %v, want %v", ran, want)
	}
}
