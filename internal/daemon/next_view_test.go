package daemon

import (
	"slices"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/domain/gate"
	"github.com/wloophq/wloop/internal/domain/task"
)

// A task with an open gate waits in Needs you and not in Next actions; one with no gate whose next step is the
// user's is in Next actions alone.
func TestATaskWithAnOpenGateIsInNeedsYouAndOneWithoutIsANextAction(t *testing.T) {
	t.Parallel()
	q := newQueueRig(t, nil)
	q.put(t, task.Task{ID: "t-0000000000000001", State: "next", Category: task.CategoryReady, Title: "answer the vendor"},
		task.Task{ID: "t-0000000000000002", State: "next", Category: task.CategoryReady, Title: "write the release notes"})
	q.open(t, "t-0000000000000001", gate.KindQuestion, "7", time.Hour)
	if got := taskIDs(q.page(t, "view=next").Tasks); !slices.Equal(got, []string{"t-0000000000000002"}) {
		t.Errorf("Next actions %v, want the task with no gate alone", got)
	}
	var queued []string
	for _, it := range q.all(t).Items {
		queued = append(queued, it.Task.ID)
	}
	if !slices.Equal(queued, []string{"t-0000000000000001"}) {
		t.Errorf("Needs you %v, want the gated task alone", queued)
	}
	var d api.Digest
	get(t, q.s, api.DigestPath+"?day=2026-10-08", &d)
	if d.Counts.Views["next"] != 1 || d.Counts.Views["needs-you"] != 1 {
		t.Errorf("digest counts %v, want the badge's Next actions and Needs you to count one each", d.Counts.Views)
	}
}

// The catalog serves each view's rail section and whether it is counted, and the sections of the rail's own
// entries; the counters leave out a view that is not counted.
func TestTheCatalogServesTheRailSectionsAndTheCountersSkipUncountedViews(t *testing.T) {
	t.Parallel()
	r := newTasksRig(t, nil)
	r.put(t, task.Task{ID: "t-done", State: "done", Category: task.CategoryDone})
	var cat api.ViewsCatalog
	get(t, r.s, api.ViewsPath, &cat)
	byID := map[string]api.TaskViewSpec{}
	for _, v := range cat.Views {
		byID[v.ID] = v
	}
	if v := byID["next"]; v.Section != "What's on you" || v.Uncounted {
		t.Errorf("next %+v, want in What's on you, counted", v)
	}
	if v := byID["logbook"]; v.Section != "Archive" || !v.Uncounted {
		t.Errorf("logbook %+v, want in the archive, uncounted", v)
	}
	if cat.Rail["plan"] != (api.RailEntrySpec{Section: "Archive", Uncounted: true}) || cat.Rail["recent"] != (api.RailEntrySpec{Section: "Archive", Uncounted: true}) {
		t.Errorf("rail %+v, want plan and recent in the archive, uncounted", cat.Rail)
	}
	var c api.Counters
	get(t, r.s, api.CountersPath, &c)
	if _, ok := c.Views["logbook"]; ok {
		t.Errorf("counters %v count the uncounted logbook", c.Views)
	}
	if _, ok := c.Views["next"]; !ok {
		t.Errorf("counters %v leave out next", c.Views)
	}
}
