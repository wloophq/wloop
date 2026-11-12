package messages_test

import (
	"context"
	"slices"
	"testing"

	"github.com/wloophq/wloop/internal/core/gates"
	"github.com/wloophq/wloop/internal/core/messages"
	"github.com/wloophq/wloop/internal/domain/gate"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/turn"
)

// TestAResumedTurnsQuestionPinsItsChainsDrafts (S4): turn 2 resumes turn 1 and asks a question whose options pin
// drafts 42 and 43, which turn 1 proposed. The question pins them: approving draft 42's card picks its option and
// withdraws draft 43, so one question posts one verdict.
func TestAResumedTurnsQuestionPinsItsChainsDrafts(t *testing.T) {
	ctx := context.Background()
	r := newRig(t)
	od := newOptionDrafts(t, r)
	od.put(heldDraft(42, r7, 1, 1), "term_1")
	od.put(heldDraft(43, r7, 1, 1), "term_1")
	b := r.started(r7)
	t1 := b.Turns[0]
	t1.State = turn.Done
	t2 := t1
	t2.Seq, t2.ID, t2.ResumeOf, t2.State = 2, "def", 1, turn.Started
	b.Turns = []turn.Turn{t1, t2}
	r.books.Put(b)
	r.record(t, r7, 2, askedRecord(draftQuestion()))
	r.live()
	r.reconcile(t, r7)
	m := r.only(t)

	pinned, err := r.svc.PinnedDrafts(ctx)
	if err != nil || pinned[42] != m.ID || pinned[43] != m.ID {
		t.Fatalf("pinned %v %v, want drafts 42 and 43 pinned by message %d", pinned, err, m.ID)
	}
	if err := r.gates.Resolve(ctx, od.gateOf(42).ID, gates.Resolution{Intent: "approve", By: task.ActorPerson}); err != nil {
		t.Fatalf("approving draft 42's card: %v", err)
	}
	if got := r.gateOf(t, m); got.Status != gate.Answered {
		t.Errorf("question %s after draft 42's approval, want answered", got.Status)
	}
	if want := []withdrawal{{43, messages.ReasonOtherOption}}; !slices.Equal(od.withdrawn, want) {
		t.Errorf("withdrawn %+v, want %+v", od.withdrawn, want)
	}
	_ = r.gates.Resolve(ctx, od.gateOf(43).ID, gates.Resolution{Intent: "approve", By: task.ActorPerson})
	if len(od.approvals) != 1 || od.approvals[0].drafted != od.gateOf(42).ID {
		t.Errorf("approvals %+v, want draft 42's alone", od.approvals)
	}
}
