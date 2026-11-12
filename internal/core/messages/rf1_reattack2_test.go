package messages_test

import (
	"testing"

	"github.com/wloophq/wloop/internal/core/messages"
	"github.com/wloophq/wloop/internal/domain/record"
	"github.com/wloophq/wloop/internal/domain/turn"
)

// The option the user clicks names only its id: the agent re-pins "approve" to a new rev under the same
// message and gate between the person reading it and clicking, and the click approves the new rev.
func TestReattackOptionRepinnedUnderTheSameGate(t *testing.T) {
	r, od, seen := draftRig(t)
	gseen := r.gateOf(t, seen)
	od.put(heldDraft(42, r7, 1, 2), "term_1", "reviser")
	q := draftQuestion()
	q.Options[0].Draft = &record.DraftRef{ID: 42, Rev: 2}
	r.record(t, r7, 1, askedRecord(q))
	r.reconcile(t, r7)
	now := r.only(t)
	gnow := r.gateOf(t, now)
	route, err := r.answer(now, messages.Answer{OptionID: "approve", Source: turn.Cockpit})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("message %d->%d gate %d->%d route %s approvals %+v", seen.ID, now.ID, gseen.ID, gnow.ID, route.Route, od.approvals)
	if gnow.ID == gseen.ID && route.Route == turn.Approved {
		t.Errorf("same gate %d, the pick approved rev 2", gnow.ID)
	}
}
