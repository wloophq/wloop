package factsgithub_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/wloophq/wloop/internal/core/change"
	coretasks "github.com/wloophq/wloop/internal/core/tasks"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
)

// TestAPageReadsTheViewItsOpenTasksEventsAndItsClosedTasksEventsOnce builds one view for every unit's facts a
// page reads, however many times each is read, and reads the events of every pull request its open tasks link at
// once; those of the pull requests its closed tasks link are read at once when the facts of one of them are.
func TestAPageReadsTheViewItsOpenTasksEventsAndItsClosedTasksEventsOnce(t *testing.T) {
	t.Parallel()
	w := newWorld()
	var es []task.Task
	for i, n := range []int{1, 2, 3} {
		w.add(n, "ana", nil)
		es = append(es, task.Task{ID: task.ID("t-" + string(rune('a'+i))), State: "next", Category: task.CategoryReady, Units: []unit.Unit{ref(n).Unit()}})
	}
	w.add(4, "ana", nil)
	w.add(5, "bea", nil)
	done := task.Task{ID: "t-y", State: "done", Category: task.CategoryDone, Units: []unit.Unit{ref(4).Unit()}}
	dropped := task.Task{ID: "t-z", State: "trash", Category: task.CategoryDropped, Units: []unit.Unit{ref(5).Unit()}}
	p := w.provider()
	ctx := coretasks.WithPage(coretasks.WithPass(context.Background(), &coretasks.Pass{Tasks: append(es, done, dropped)}))
	for range 2 {
		for _, e := range es {
			if _, err := p.Facts(ctx, e.Units[0]); err != nil {
				t.Fatal(err)
			}
		}
	}
	if w.snapReads != 1 || w.eventReads != 1 {
		t.Errorf("%d views built and %d event reads for the facts of three pull requests read twice in a page, want 1 and 1",
			w.snapReads, w.eventReads)
	}
	for _, n := range []int{4, 5, 4} {
		if _, err := p.Facts(ctx, ref(n).Unit()); err != nil {
			t.Fatal(err)
		}
	}
	if w.eventReads != 2 {
		t.Errorf("the facts of two closed tasks' pull requests: %d event reads, want one more for both", w.eventReads)
	}
}

// TestASweepsPassReadsTheCurrentView reads, in a pass that serves no page, the view each call finds current, as
// the sweep needs once its List renewed it.
func TestASweepsPassReadsTheCurrentView(t *testing.T) {
	t.Parallel()
	w := newWorld()
	w.add(1, "ana", nil)
	p := w.provider()
	ctx := coretasks.WithPass(context.Background(), &coretasks.Pass{Tasks: []task.Task{{ID: "t-a", State: "next", Category: task.CategoryReady, Units: []unit.Unit{ref(1).Unit()}}}})
	for range 2 {
		if _, err := p.Facts(ctx, ref(1).Unit()); err != nil {
			t.Fatal(err)
		}
	}
	if w.snapReads != 2 {
		t.Errorf("%d views built for two calls in a sweep's pass over no change log, want one each", w.snapReads)
	}
}

// TestAPullRequestNoClosedTaskLinksReadsItsOwnEventsAlone reads, for the facts of a pull request no closed task of
// the pass links, its own events, and leaves those of the closed tasks' pull requests unread.
func TestAPullRequestNoClosedTaskLinksReadsItsOwnEventsAlone(t *testing.T) {
	t.Parallel()
	w := newWorld()
	w.add(1, "ana", nil)
	w.add(9, "bea", nil)
	var closed []task.Task
	for i, n := range []int{4, 5, 6} {
		w.add(n, "ana", nil)
		closed = append(closed, task.Task{ID: task.ID("t-" + string(rune('x'+i))), State: "done", Category: task.CategoryDone, Units: []unit.Unit{ref(n).Unit()}})
	}
	open := task.Task{ID: "t-a", State: "next", Category: task.CategoryReady, Units: []unit.Unit{ref(1).Unit()}}
	p := w.provider()
	ctx := coretasks.WithPage(coretasks.WithPass(context.Background(), &coretasks.Pass{Tasks: append(closed, open)}))
	if _, err := p.Facts(ctx, ref(9).Unit()); err != nil {
		t.Fatal(err)
	}
	if w.eventRefs != 2 {
		t.Errorf("the facts of a pull request no task links asked for the events of %d pull requests, want its own and the open task's", w.eventRefs)
	}
}

// TestAPassWithNoChangeLogReadsEachCallsOwnEvents reads, in a pass that serves no page and over no change log,
// where each call builds its own view, the events of the pull request each call asks for alone.
func TestAPassWithNoChangeLogReadsEachCallsOwnEvents(t *testing.T) {
	t.Parallel()
	w := newWorld()
	var es []task.Task
	for i, n := range []int{1, 2, 3, 4} {
		w.add(n, "ana", nil)
		e := task.Task{ID: task.ID("t-" + string(rune('a'+i))), State: "next", Category: task.CategoryReady, Units: []unit.Unit{ref(n).Unit()}}
		if n > 2 {
			e.State, e.Category = "done", task.CategoryDone
		}
		es = append(es, e)
	}
	p := w.provider()
	ctx := coretasks.WithPass(context.Background(), &coretasks.Pass{Tasks: es})
	for _, e := range es {
		if _, err := p.Facts(ctx, e.Units[0]); err != nil {
			t.Fatal(err)
		}
	}
	if w.eventRefs != len(es) {
		t.Errorf("%d calls with no view to share asked for the events of %d pull requests, want one each", len(es), w.eventRefs)
	}
}

// TestAPassOutsideAPageReadsItsOpenTasksEventsOnce reads, in a pass that serves no page, the events of every
// pull request its open tasks link at once, for every view the calls find current.
func TestAPassOutsideAPageReadsItsOpenTasksEventsOnce(t *testing.T) {
	t.Parallel()
	w := newWorld()
	var es []task.Task
	for i, n := range []int{1, 2, 3} {
		w.add(n, "ana", nil)
		es = append(es, task.Task{ID: task.ID("t-" + string(rune('a'+i))), State: "next", Category: task.CategoryReady, Units: []unit.Unit{ref(n).Unit()}})
	}
	p := w.providerWith(change.NewLog(8, nil))
	ctx := coretasks.WithPass(context.Background(), &coretasks.Pass{Tasks: es})
	for _, e := range es {
		if _, err := p.Facts(ctx, e.Units[0]); err != nil {
			t.Fatal(err)
		}
	}
	if w.snapReads != 1 || w.eventReads != 1 {
		t.Errorf("%d views built and %d event reads for the facts of three pull requests in a pass, want 1 and 1", w.snapReads, w.eventReads)
	}
}

// TestAPrimedViewGivesTheFactsOfAViewReadAlone holds a pull request's facts in a pass, whose view read its open
// tasks' events and reports at once, equal to those a call outside any pass reads, a pull request the pass does
// not hold included.
func TestAPrimedViewGivesTheFactsOfAViewReadAlone(t *testing.T) {
	t.Parallel()
	w := newWorld()
	var es []task.Task
	for i, n := range []int{1, 2} {
		w.add(n, "ana", nil)
		es = append(es, task.Task{ID: task.ID("t-" + string(rune('a'+i))), State: "next", Category: task.CategoryReady, Units: []unit.Unit{ref(n).Unit()}})
	}
	w.add(3, "bea", nil)
	p := w.providerWith(change.NewLog(8, nil))
	pass := coretasks.WithPass(context.Background(), &coretasks.Pass{Tasks: es})
	for _, n := range []int{1, 2, 3} {
		in, err := p.Facts(pass, ref(n).Unit())
		if err != nil {
			t.Fatal(err)
		}
		alone, err := w.provider().Facts(context.Background(), ref(n).Unit())
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(in, alone) {
			t.Errorf("pull request %d: in a pass %v, alone %v", n, in, alone)
		}
	}
}
