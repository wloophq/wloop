package messages_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/wloophq/wloop/internal/core/gates"
	"github.com/wloophq/wloop/internal/core/messages"
	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/gate"
	"github.com/wloophq/wloop/internal/domain/record"
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

// TestAnotherUnitsTurnPinsNothing: draft 60 is on r7, proposed by turn 1 of r8. Turn 1 of r7 asks a question that
// points to it: turn seqs are per unit, so the question pins nothing and draft 60's card approves on its own.
func TestAnotherUnitsTurnPinsNothing(t *testing.T) {
	ctx := context.Background()
	r := newRig(t)
	od := newOptionDrafts(t, r)
	od.put(draft.Draft{ID: 60, Unit: r7, State: draft.Held, Rev: 1, Text: "another session's comment",
		Origin: draft.Origin{Kind: draft.OriginTurn, TurnSeq: 1, Unit: r8, Session: "term_9"}}, "term_9")
	r.started(r7)
	r.record(t, r7, 1, askedRecord(&record.Question{Kind: "choice", Text: "Post it?", Options: []record.Option{
		{ID: "post", Label: "Post", Consequence: "posts it", Draft: &record.DraftRef{ID: 60, Rev: 1}}, {ID: "hold", Label: "Hold", Consequence: "nothing"}}}))
	r.live()
	r.reconcile(t, r7)
	if pinned, err := r.svc.PinnedDrafts(ctx); err != nil || pinned[60] != 0 {
		t.Fatalf("pinned %v %v: r7's question pins draft 60, which r8's turn proposed", pinned, err)
	}
	if err := r.gates.Resolve(ctx, od.gateOf(60).ID, gates.Resolution{Intent: "approve", By: task.ActorPerson}); err != nil {
		t.Fatalf("approving draft 60's card: %v", err)
	}
}

// TestAReplacingTurnsQuestionPinsItsPredecessorsDrafts: turn 2 replaced asked turn 1 (replace_if_moved) and asks
// again, pinning drafts 42 and 43 that turn 1 proposed. Approving draft 42's card picks its option and withdraws 43.
func TestAReplacingTurnsQuestionPinsItsPredecessorsDrafts(t *testing.T) {
	ctx := context.Background()
	r := newRig(t)
	od := newOptionDrafts(t, r)
	od.put(heldDraft(42, r7, 1, 1), "term_1")
	od.put(heldDraft(43, r7, 1, 1), "term_1")
	b := r.started(r7)
	t1 := b.Turns[0]
	t2 := t1
	t1.State, t1.ReplacedBy, t1.Before = turn.Replaced, 2, turn.Asked
	t2.Seq, t2.ID, t2.State = 2, "def", turn.Started
	b.Turns = []turn.Turn{t1, t2}
	r.books.Put(b)
	r.record(t, r7, 2, askedRecord(draftQuestion()))
	r.live()
	r.reconcile(t, r7)
	m := r.only(t)
	if err := r.gates.Resolve(ctx, od.gateOf(42).ID, gates.Resolution{Intent: "approve", By: task.ActorPerson}); err != nil {
		t.Fatalf("approving draft 42's card: %v", err)
	}
	_ = r.gates.Resolve(ctx, od.gateOf(43).ID, gates.Resolution{Intent: "approve", By: task.ActorPerson})
	if got := r.gateOf(t, m); got.Status != gate.Answered || len(od.approvals) != 1 || od.approvals[0].drafted != od.gateOf(42).ID {
		t.Errorf("question %s, approvals %+v: want the question answered by draft 42's approval alone", got.Status, od.approvals)
	}
}

// TestAQuestionDecidesItsPinnedDraftsRetry: while a question pins drafts 42 and 43, a retry of either is refused;
// once its answer picked 42, the question no longer decides it.
func TestAQuestionDecidesItsPinnedDraftsRetry(t *testing.T) {
	ctx := context.Background()
	r, od, m := draftRig(t)
	for _, id := range []int64{42, 43} {
		if err := r.gates.Decides(ctx, od.gateOf(id)); !errors.Is(err, gates.ErrRefused) {
			t.Fatalf("a retry of draft %d its open question pins: %v", id, err)
		}
	}
	if _, err := r.answer(m, messages.Answer{OptionID: "approve", Source: turn.Cockpit}); err != nil {
		t.Fatal(err)
	}
	if err := r.gates.Decides(ctx, od.gateOf(42)); err != nil {
		t.Fatalf("a retry of draft 42, which the answer picked: %v", err)
	}
}
