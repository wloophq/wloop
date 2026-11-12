package messages_test

import (
	"context"
	"slices"
	"strconv"
	"testing"

	"github.com/wloophq/wloop/internal/core/gates"
	"github.com/wloophq/wloop/internal/core/messages"
	"github.com/wloophq/wloop/internal/domain/gate"
	"github.com/wloophq/wloop/internal/domain/task"
)

// The gates' option hook reads r.Answer for any intent of a question gate, and ApproveOption approves the
// draft gate before it resolves the question with r. A resolution that does not answer (dismiss, continue,
// an intent the gate does not offer) but carries an option id as its answer text approves the pinned draft.
func TestReattackNonAnswerResolutionApprovesThePin(t *testing.T) {
	for _, intent := range []string{"dismiss", "continue", "no-such-intent"} {
		r, od, m := draftRig(t)
		ctx := context.Background()
		open, err := r.gateStore.Where(ctx, gates.Query{Kind: m.Kind.Gate(), Unit: m.Unit})
		if err != nil {
			t.Fatal(err)
		}
		i := slices.IndexFunc(open, func(g gate.Gate) bool { return g.Subject == strconv.FormatInt(m.ID, 10) })
		rerr := r.gates.Resolve(messages.WithDelivery(ctx, &messages.Delivery{}), open[i].ID,
			gates.Resolution{Intent: intent, Answer: "approve", By: task.ActorPerson})
		if dg := od.gateOf(42); dg.Status == gate.Approved || len(od.approvals) > 0 {
			t.Errorf("%s (err %v): draft gate %+v approved, approvals %+v", intent, rerr, dg.Status, od.approvals)
		}
	}
}
