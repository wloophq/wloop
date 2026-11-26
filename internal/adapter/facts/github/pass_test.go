package factsgithub_test

import (
	"context"
	"testing"

	coretasks "github.com/wloophq/wloop/internal/core/tasks"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
)

// TestAPassReadsTheViewAndEveryUnitsEventsOnce builds one view for every unit's facts a pass reads, however many
// times each is read, and reads the events of every pull request the pass's tasks link at once.
func TestAPassReadsTheViewAndEveryUnitsEventsOnce(t *testing.T) {
	t.Parallel()
	w := newWorld()
	var es []task.Task
	for i, n := range []int{1, 2, 3} {
		w.add(n, "ana", nil)
		es = append(es, task.Task{ID: task.ID("t-" + string(rune('a'+i))), Units: []unit.Unit{ref(n).Unit()}})
	}
	p := w.provider()
	ctx := coretasks.WithPass(context.Background(), &coretasks.Pass{Tasks: es})
	for range 2 {
		for _, e := range es {
			if _, err := p.Facts(ctx, e.Units[0]); err != nil {
				t.Fatal(err)
			}
		}
	}
	if w.snapReads != 1 || w.eventReads != 1 {
		t.Errorf("%d views built and %d event reads for the facts of three pull requests read twice in a pass, want 1 and 1",
			w.snapReads, w.eventReads)
	}
}
