package tasks_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	coretasks "github.com/wloophq/wloop/internal/core/tasks"
	"github.com/wloophq/wloop/internal/core/tasks/taskstest"
	"github.com/wloophq/wloop/internal/core/wakes/wakestest"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/workflow"
)

// setsRig is a rig whose views read each task's facts through the pass, counting the reads, and whose ready
// view sorts by title.
type setsRig struct {
	*rig
	reads  int
	passes []*coretasks.Pass
}

func newSetsRig() *setsRig {
	r := &setsRig{rig: newRig()}
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
	sort := func(_ context.Context, view string, ts []task.Task) error {
		if view == "ready" {
			slices.SortStableFunc(ts, func(a, b task.Task) int { return strings.Compare(a.Title, b.Title) })
		}
		return nil
	}
	r.svc = coretasks.New(coretasks.Deps{Store: r.store, Items: r.items, Facts: r.facts, Me: me, Clock: r.clock, States: taskstest.BundledStates,
		Authority: taskstest.BundledStates.Authority, Wakes: wakestest.NewStore(), Views: r.lens.Views, ViewEval: eval, ViewSort: sort})
	return r
}

func (r *setsRig) seed(t *testing.T) {
	r.put(t, task.Task{ID: "in-1", State: "inbox", Category: task.CategoryInbox, Title: "call Ada"})
	r.put(t, task.Task{ID: "nx-1", State: "next", Category: task.CategoryReady, Title: "write the brief", Props: task.Props{"focus": {Text: "on"}}})
	r.put(t, task.Task{ID: "nx-2", State: "next", Category: task.CategoryReady, Title: "answer Brook", Created: t0.Add(time.Hour), Updated: t0.Add(time.Hour)})
	r.put(t, task.Task{ID: "wt-1", State: "waiting", Category: task.CategoryWaiting, Title: "contract from Brook"})
}

func ids(ts []task.Task) []task.ID {
	out := make([]task.ID, len(ts))
	for i, e := range ts {
		out[i] = e.ID
	}
	return out
}

func TestSetsEvaluatesEveryViewInOnePass(t *testing.T) {
	r := newSetsRig()
	r.seed(t)
	got, err := r.svc.Sets(context.Background(), []task.View{"ready", "waiting", "starred", "inbox"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if r.reads != 4 || len(r.passes) != 1 {
		t.Fatalf("facts read %d times over %d passes, want once per task (4) in one pass", r.reads, len(r.passes))
	}
	if !slices.Equal(ids(got["waiting"]), []task.ID{"wt-1"}) || !slices.Equal(ids(got["starred"]), []task.ID{"nx-1"}) {
		t.Fatalf("sets %v", got)
	}
}

func TestSetsSortsAsTheView(t *testing.T) {
	r := newSetsRig()
	r.seed(t)
	got, err := r.svc.Sets(context.Background(), []task.View{"ready"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if want := []task.ID{"nx-2", "nx-1"}; !slices.Equal(ids(got["ready"]), want) {
		t.Fatalf("ready %v, want %v, the view's own order", ids(got["ready"]), want)
	}
}

func TestSetsKeepCalledOncePerTask(t *testing.T) {
	r := newSetsRig()
	r.seed(t)
	calls := map[task.ID]int{}
	keep := func(_ context.Context, e task.Task) (bool, error) {
		calls[e.ID]++
		return strings.Contains(e.Title, "brief") || strings.Contains(e.Title, "Brook"), nil
	}
	got, err := r.svc.Sets(context.Background(), []task.View{"ready", "starred", "waiting"}, keep)
	if err != nil {
		t.Fatal(err)
	}
	if calls["nx-1"] != 1 {
		t.Fatalf("keep called %d times on a task in two views", calls["nx-1"])
	}
	if !slices.Equal(ids(got["ready"]), []task.ID{"nx-2", "nx-1"}) || !slices.Equal(ids(got["waiting"]), []task.ID{"wt-1"}) {
		t.Fatalf("sets %v", got)
	}
}

func TestSetsUnknownView(t *testing.T) {
	r := newSetsRig()
	if _, err := r.svc.Sets(context.Background(), []task.View{"ready", "nope"}, nil); !errors.Is(err, coretasks.ErrNoView) {
		t.Fatalf("err = %v, want ErrNoView", err)
	}
}

func TestCountsLeaveOutUncountedViews(t *testing.T) {
	r := newSetsRig()
	r.lens.add(testView{coretasks.ViewSpec{ID: "home-ready", Over: task.OverTasks, Uncounted: true}, func(e task.Task) bool { return e.Category == task.CategoryReady }})
	r.seed(t)
	c, err := r.svc.Counts(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if _, counted := c.Views["home-ready"]; counted || c.Views["ready"] != 2 {
		t.Fatalf("counts %v", c.Views)
	}
	sets, _ := r.svc.Sets(context.Background(), []task.View{"home-ready"}, nil)
	if len(sets["home-ready"]) != 2 {
		t.Fatalf("Sets of an uncounted view: %v", sets)
	}
}

func TestCountsKeepBadgeAndReviewViewsOffTheRail(t *testing.T) {
	r := newSetsRig()
	ready := func(e task.Task) bool { return e.Category == task.CategoryReady }
	r.lens.add(testView{coretasks.ViewSpec{ID: "badged-ready", Over: task.OverTasks, Uncounted: true}, ready})
	r.lens.add(testView{coretasks.ViewSpec{ID: "reviewed-ready", Over: task.OverTasks, Uncounted: true}, ready})
	r.lens.badge = []task.View{"badged-ready"}
	r.lens.review = []task.View{"reviewed-ready"}
	r.seed(t)
	c, err := r.svc.Counts(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if c.Badge != 2 || c.Views["badged-ready"] != 2 || c.Views["reviewed-ready"] != 2 {
		t.Fatalf("badge %d views %v: a view the badge or a review step names counts, on the rail or off it", c.Badge, c.Views)
	}
}

func TestCountsMatchSets(t *testing.T) {
	ctx := context.Background()
	r := newSetsRig()
	r.seed(t)
	r.svc.RegisterView(task.OverGates, "draft:", outbox{drafts: []task.Task{{ID: "draft:1", Title: "reply"}}})
	c, err := r.svc.Counts(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	var all []task.View
	for _, spec := range r.lens.Views().List {
		all = append(all, spec.ID)
	}
	sets, err := r.svc.Sets(ctx, all, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range all {
		if c.Views[v] != len(sets[v]) {
			t.Errorf("%s counts %d, Sets lists %d", v, c.Views[v], len(sets[v]))
		}
	}
}
