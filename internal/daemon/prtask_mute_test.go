package daemon

import (
	"context"
	"testing"

	"github.com/wloophq/wloop/internal/domain/turn"
)

// TestMutingAFollowedPRNoTaskHoldsMutesItsTask follows a pull request the loop works on, which asks nothing: it still has
// its one task, which the mute defers, and which the next sync keeps deferred instead of making another.
func TestMutingAFollowedPRNoTaskHoldsMutesItsTask(t *testing.T) {
	t.Parallel()
	r := newNotifyRig(t)
	r.asks(t, turn.Started, draftQuestion("publish?"))
	if row := r.settle(t, r7u); row.Kind != "loop_working" {
		t.Fatalf("the row %s, want the loop working on it", row.Kind)
	}
	before, _, _ := prTask(t, r.s, r7)
	if rec := post(t, r.s, "/api/units/github/acme%2Fapi%237/mute", `{"until":"2d"}`); rec.Code != 200 {
		t.Fatalf("mute: %d %s", rec.Code, rec.Body)
	}
	r.settle(t, r7u)
	ts, err := r.db.Tasks().TasksOf(context.Background(), r7u)
	if err != nil || len(ts) != 1 || ts[0].ID != before.ID {
		t.Fatalf("the pull request's tasks after the mute %+v %v, want %s alone", ts, err, before.ID)
	}
	if w, ok, err := r.db.Wakes().Of(context.Background(), before.ID); err != nil || !ok || w.Until.IsZero() || ts[0].Category != "deferred" {
		t.Fatalf("the task %s, its wake %+v %v %v", ts[0].Category, w, ok, err)
	}
}
