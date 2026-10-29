package tasks_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	coretasks "github.com/wloophq/wloop/internal/core/tasks"
	"github.com/wloophq/wloop/internal/domain/task"
)

// countedView is a source of n tasks over gates.
type countedView struct{ n int }

func (v countedView) Tasks(ctx context.Context, q task.Query) ([]task.Task, error) {
	out := make([]task.Task, v.n)
	for i := range out {
		out[i].ID = task.ID(fmt.Sprint("draft:", i+1))
	}
	return out, nil
}
func (v countedView) Task(ctx context.Context, id task.ID) (task.Task, bool, error) {
	return task.Task{}, false, nil
}

// reviewLens adds to the test lens the views the review walks beyond it: someday, and the projects with no
// ready child; the review walks them in the bundled order.
func reviewLens(l *testLens) {
	l.add(testView{coretasks.ViewSpec{ID: "someday", Over: task.OverTasks, Paged: true}, func(e task.Task) bool { return e.State == "someday" }})
	l.add(testView{coretasks.ViewSpec{ID: "projects-to-review", Over: task.OverTasks}, nil})
	l.review = []task.View{"inbox", "drafts", "waiting", "projects-to-review", "someday", "starred"}
}

// reviewRig has 2 in the Inbox, 1 waiting, nothing for someday, 1 starred and 1 project without a next action.
func reviewRig(t *testing.T) *projectRig {
	r := newProjectRig()
	r.put(t, task.Task{ID: "i1", State: "inbox", Category: task.CategoryInbox, Title: "i1", Origin: task.OriginUser})
	r.put(t, task.Task{ID: "i2", State: "inbox", Category: task.CategoryInbox, Title: "i2", Origin: task.OriginUser})
	r.put(t, task.Task{ID: "w1", State: "waiting", Category: task.CategoryWaiting, Title: "w1", Origin: task.OriginUser})
	r.put(t, task.Task{ID: "f1", State: "next", Category: task.CategoryReady, Title: "f1", Origin: task.OriginUser, Props: task.Props{"focus": {Text: "on"}}})
	r.project(t, "p", nil)
	reviewLens(r.lens)
	r.lens.views[len(r.lens.views)-1].in = func(e task.Task) bool { return e.IsProject() && e.Open() && !r.hasReadyChild(e.ID) }
	return r
}

func (r *projectRig) hasReadyChild(id task.ID) bool {
	all, _ := r.store.Tasks(context.Background(), task.Query{})
	return slices.ContainsFunc(all, func(e task.Task) bool { return e.Parent == id && e.Category == task.CategoryReady })
}

func stepIDs(rv coretasks.Review) []string {
	var out []string
	for _, s := range rv.Steps {
		out = append(out, string(s.View))
	}
	return out
}

func stepOf(t *testing.T, rv coretasks.Review, id string) coretasks.ReviewStep {
	t.Helper()
	i := slices.IndexFunc(rv.Steps, func(s coretasks.ReviewStep) bool { return string(s.View) == id })
	if i < 0 {
		t.Fatalf("no step %s in %v", id, stepIDs(rv))
	}
	return rv.Steps[i]
}

func TestReviewSteps(t *testing.T) {
	r := reviewRig(t)
	r.svc.RegisterView(task.OverGates, "draft:", countedView{n: 3})
	rv, err := r.svc.Review(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"inbox", "drafts", "waiting", "projects-to-review", "someday", "starred"}; !slices.Equal(stepIDs(rv), want) {
		t.Fatalf("steps %v, want %v", stepIDs(rv), want)
	}
	want := map[task.View]int{"inbox": 2, "drafts": 3, "waiting": 1, "projects-to-review": 1, "someday": 0, "starred": 1}
	for _, s := range rv.Steps {
		if s.Count != want[s.View] {
			t.Errorf("step %s: count %d, want %d", s.View, s.Count, want[s.View])
		}
	}
	if rv.ID == "" || !rv.StartedAt.Equal(t0) || rv.Done {
		t.Errorf("review %s started %v done %v", rv.ID, rv.StartedAt, rv.Done)
	}
}

func TestStepDone(t *testing.T) {
	r := reviewRig(t)
	ctx := context.Background()
	rv, err := r.svc.Review(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for id, done := range map[string]bool{"inbox": false, "drafts": true, "waiting": false, "projects-to-review": false, "someday": true, "starred": false} {
		if got := stepOf(t, rv, id).Done; got != done {
			t.Errorf("step %s starts done %v, want %v", id, got, done)
		}
	}
	got, err := r.svc.ReviewStepDone(ctx, "inbox")
	if err != nil {
		t.Fatal(err)
	}
	if !stepOf(t, got, "inbox").Done || got.ID != rv.ID {
		t.Fatalf("inbox not done in %+v", got)
	}
	r.put(t, task.Task{ID: "s1", State: "someday", Category: task.CategoryDeferred, Title: "s1", Origin: task.OriginUser})
	again, err := r.svc.Review(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if again.ID != rv.ID || !stepOf(t, again, "inbox").Done || stepOf(t, again, "inbox").Count != 2 || !stepOf(t, again, "someday").Done {
		t.Errorf("the review did not keep its steps: %+v", again)
	}
	if _, err := r.svc.ReviewStepDone(ctx, "nope"); !errors.Is(err, coretasks.ErrArgs) {
		t.Errorf("an unknown step: %v, want ErrArgs", err)
	}
}

func TestReviewCompletes(t *testing.T) {
	r := reviewRig(t)
	ctx := context.Background()
	first, err := r.svc.Review(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var last coretasks.Review
	for _, id := range []string{"inbox", "waiting", "projects-to-review"} {
		if last, err = r.svc.ReviewStepDone(ctx, id); err != nil {
			t.Fatal(err)
		}
		if last.Done {
			t.Fatalf("done after %s with focus open", id)
		}
	}
	r.clock.t = t0.Add(time.Hour)
	if last, err = r.svc.ReviewStepDone(ctx, "starred"); err != nil {
		t.Fatal(err)
	}
	if !last.Done || last.ID != first.ID {
		t.Fatalf("all steps done but the review is not: %+v", last)
	}
	runs := r.projects.Runs()
	if len(runs) != 1 || runs[0].ID != first.ID || !runs[0].FinishedAt.Equal(r.clock.t) || len(runs[0].Done) != 6 {
		t.Fatalf("recorded runs %+v", runs)
	}
	next, err := r.svc.Review(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if next.ID == first.ID || next.Done || stepOf(t, next, "inbox").Done || !next.StartedAt.Equal(r.clock.t) {
		t.Errorf("the next review did not start empty: %+v", next)
	}
}

func TestOutboxStepWithoutDrafts(t *testing.T) {
	ctx := context.Background()
	r := reviewRig(t)
	rv, err := r.svc.Review(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if st := stepOf(t, rv, "drafts"); st.Count != 0 || !st.Done {
		t.Errorf("no outbox source: %+v", st)
	}
	r = reviewRig(t)
	r.svc.RegisterView(task.OverGates, "draft:", countedView{})
	if rv, err = r.svc.Review(ctx); err != nil {
		t.Fatal(err)
	}
	if st := stepOf(t, rv, "drafts"); st.Count != 0 || !st.Done {
		t.Errorf("an empty outbox: %+v", st)
	}
}

func TestEmptyReviewIsDoneAndNotRecorded(t *testing.T) {
	r := newProjectRig()
	rv, err := r.svc.Review(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !rv.Done || len(r.projects.Runs()) != 0 {
		t.Errorf("an empty review: done %v, runs %v", rv.Done, r.projects.Runs())
	}
}

func TestReviewServedOnlyOnceRegistered(t *testing.T) {
	r := newRig()
	ctx := context.Background()
	if r.svc.ServesReview() {
		t.Error("review served before ServeProjects")
	}
	if _, err := r.svc.Review(ctx); !errors.Is(err, coretasks.ErrArgs) {
		t.Errorf("review before ServeProjects: %v, want ErrArgs", err)
	}
	if p := newProjectRig(); !p.svc.ServesReview() {
		t.Error("review not served after ServeProjects")
	}
}
