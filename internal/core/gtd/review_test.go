package gtd_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	coregtd "github.com/wloophq/wloop/internal/core/gtd"
	"github.com/wloophq/wloop/internal/domain/gtd"
)

type countedView struct{ n int }

func (v countedView) Entries(ctx context.Context, q gtd.Query) ([]gtd.Entry, error) { return nil, nil }
func (v countedView) Entry(ctx context.Context, id gtd.ID) (gtd.Entry, bool, error) {
	return gtd.Entry{}, false, nil
}
func (v countedView) Count(ctx context.Context, area string) (int, error) { return v.n, nil }

// reviewRig has 2 in the Inbox, 1 waiting, nothing for someday, 1 starred and 1 project without a next action.
func reviewRig(t *testing.T) *projectRig {
	r := newProjectRig()
	r.put(t, gtd.Entry{ID: "i1", Kind: gtd.KindCapture, State: gtd.Inbox, Title: "i1", Origin: gtd.OriginUser})
	r.put(t, gtd.Entry{ID: "i2", Kind: gtd.KindCapture, State: gtd.Inbox, Title: "i2", Origin: gtd.OriginUser})
	r.put(t, gtd.Entry{ID: "w1", Kind: gtd.KindCapture, State: gtd.Waiting, Title: "w1", Origin: gtd.OriginUser})
	r.put(t, gtd.Entry{ID: "f1", Kind: gtd.KindCapture, State: gtd.Next, Title: "f1", Origin: gtd.OriginUser, Focus: gtd.Focus{On: true}})
	r.project(t, "p", nil)
	return r
}

func stepIDs(rv coregtd.Review) []string {
	var out []string
	for _, s := range rv.Steps {
		out = append(out, s.ID)
	}
	return out
}

func stepOf(t *testing.T, rv coregtd.Review, id string) coregtd.ReviewStep {
	t.Helper()
	i := slices.IndexFunc(rv.Steps, func(s coregtd.ReviewStep) bool { return s.ID == id })
	if i < 0 {
		t.Fatalf("no step %s in %v", id, stepIDs(rv))
	}
	return rv.Steps[i]
}

func TestReviewSteps(t *testing.T) {
	r := reviewRig(t)
	r.svc.RegisterView(gtd.ViewOutbox, "draft:", countedView{n: 3})
	rv, err := r.svc.Review(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"inbox", "outbox", "waiting", "projects", "someday", "focus"}; !slices.Equal(stepIDs(rv), want) {
		t.Fatalf("steps %v, want %v", stepIDs(rv), want)
	}
	want := map[string]struct {
		view  gtd.View
		count int
	}{
		"inbox": {gtd.ViewInbox, 2}, "outbox": {gtd.ViewOutbox, 3}, "waiting": {gtd.ViewWaiting, 1},
		"projects": {gtd.ViewReview, 1}, "someday": {gtd.ViewSomeday, 0}, "focus": {gtd.ViewFocus, 1},
	}
	for _, s := range rv.Steps {
		w := want[s.ID]
		if s.View != w.view || s.Count != w.count {
			t.Errorf("step %s: view %s count %d, want %s %d", s.ID, s.View, s.Count, w.view, w.count)
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
	for id, done := range map[string]bool{"inbox": false, "outbox": true, "waiting": false, "projects": false, "someday": true, "focus": false} {
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
	r.put(t, gtd.Entry{ID: "s1", Kind: gtd.KindCapture, State: gtd.Someday, Title: "s1", Origin: gtd.OriginUser})
	again, err := r.svc.Review(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if again.ID != rv.ID || !stepOf(t, again, "inbox").Done || stepOf(t, again, "inbox").Count != 2 || !stepOf(t, again, "someday").Done {
		t.Errorf("the review did not keep its steps: %+v", again)
	}
	if _, err := r.svc.ReviewStepDone(ctx, "nope"); !errors.Is(err, coregtd.ErrArgs) {
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
	var last coregtd.Review
	for _, id := range []string{"inbox", "waiting", "projects"} {
		if last, err = r.svc.ReviewStepDone(ctx, id); err != nil {
			t.Fatal(err)
		}
		if last.Done {
			t.Fatalf("done after %s with focus open", id)
		}
	}
	r.clock.t = t0.Add(time.Hour)
	if last, err = r.svc.ReviewStepDone(ctx, "focus"); err != nil {
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
	if st := stepOf(t, rv, "outbox"); st.Count != 0 || !st.Done {
		t.Errorf("no outbox source: %+v", st)
	}
	r = reviewRig(t)
	r.svc.RegisterView(gtd.ViewOutbox, "draft:", countedView{})
	if rv, err = r.svc.Review(ctx); err != nil {
		t.Fatal(err)
	}
	if st := stepOf(t, rv, "outbox"); st.Count != 0 || !st.Done {
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
	if r.svc.Serves(gtd.ViewReview) {
		t.Error("review served before ServeProjects")
	}
	if _, err := r.svc.Review(ctx); !errors.Is(err, coregtd.ErrArgs) {
		t.Errorf("review before ServeProjects: %v, want ErrArgs", err)
	}
	for _, v := range append(slices.Clone(gtd.StoredViews), gtd.ViewOutbox) {
		if !r.svc.Serves(v) {
			t.Errorf("%s not served", v)
		}
	}
	if r.svc.Serves(gtd.ViewOpen) || r.svc.Serves("bogus") {
		t.Error("open or an unknown view served")
	}
	p := newProjectRig()
	if !p.svc.Serves(gtd.ViewReview) {
		t.Error("review not served after ServeProjects")
	}
}
