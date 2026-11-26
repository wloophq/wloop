package factsgithub_test

import (
	"context"
	"testing"

	coretasks "github.com/wloophq/wloop/internal/core/tasks"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
)

// TestAPageReadsTheViewAndItsOpenTasksEventsOnce builds one view for every unit's facts a page reads, however
// many times each is read, and reads the events of every pull request its open tasks link at once; a pull
// request only a closed task links is read when its facts are.
func TestAPageReadsTheViewAndItsOpenTasksEventsOnce(t *testing.T) {
	t.Parallel()
	w := newWorld()
	var es []task.Task
	for i, n := range []int{1, 2, 3} {
		w.add(n, "ana", nil)
		es = append(es, task.Task{ID: task.ID("t-" + string(rune('a'+i))), State: "next", Category: task.CategoryReady, Units: []unit.Unit{ref(n).Unit()}})
	}
	w.add(4, "ana", nil)
	done := task.Task{ID: "t-z", State: "done", Category: task.CategoryDone, Units: []unit.Unit{ref(4).Unit()}}
	p := w.provider()
	ctx := coretasks.WithPage(coretasks.WithPass(context.Background(), &coretasks.Pass{Tasks: append(es, done)}))
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
	if _, err := p.Facts(ctx, ref(4).Unit()); err != nil || w.eventReads != 2 {
		t.Errorf("the facts of a closed task's pull request: %d event reads, %v, want one more", w.eventReads, err)
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
