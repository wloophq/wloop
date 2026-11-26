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
