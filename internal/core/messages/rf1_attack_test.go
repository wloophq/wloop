package messages_test

import (
	"context"
	"testing"

	"github.com/wloophq/wloop/internal/core/gates"
	"github.com/wloophq/wloop/internal/core/messages"
	"github.com/wloophq/wloop/internal/domain/gate"
	"github.com/wloophq/wloop/internal/domain/turn"
)

// trustingGates approves whatever rev it is handed, as gates.Service.ApproveOption (still a T3 panic)
// would if its rev check read a stale drafted gate.
type trustingGates struct{ *optionDrafts }

func (o trustingGates) ApproveOption(ctx context.Context, q gate.Gate, r gates.Resolution, drafted gate.Gate, rev int64) error {
	drafted.ApprovedRev = rev
	return o.optionDrafts.ApproveOption(ctx, q, r, drafted, rev)
}

func TestAttackPinnedRevOneApprovesRevTwo(t *testing.T) {
	r, od, m := draftRig(t)
	od.put(heldDraft(42, r7, 1, 2), "term_1")
	r.svc.Drafts = trustingGates{od}
	route, err := r.answer(m, messages.Answer{OptionID: "approve", Source: turn.Cockpit})
	if err != nil {
		t.Fatal(err)
	}
	if route.Route == turn.Approved {
		t.Fatalf("option pinned to rev 1 approved draft 42 at rev %d: messages checks no rev of its own (%+v)", od.drafts[42].Rev, od.approvals)
	}
}
