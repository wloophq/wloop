package messages_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/wloophq/wloop/internal/core/gates"
	"github.com/wloophq/wloop/internal/core/messages"
	"github.com/wloophq/wloop/internal/domain/gate"
	"github.com/wloophq/wloop/internal/domain/task"
)

// TestApprovingAPinnedDraftPicksItsOption runs the sequence a person ran on a task that showed an open question
// and the two drafts its options pin as cards: they approved the first draft's card, then the second's. The
// first approval picks the option that pins it: the question is answered with it, its draft approved and the
// other option's draft withdrawn, so the second approval finds nothing to approve and one review posts.
func TestApprovingAPinnedDraftPicksItsOption(t *testing.T) {
	ctx := context.Background()
	r, od, m := draftRig(t)
	if err := r.gates.Resolve(ctx, od.gateOf(42).ID, gates.Resolution{Intent: "approve", By: task.ActorPerson}); err != nil {
		t.Fatalf("approving draft 42's card: %v", err)
	}
	q := r.gateOf(t, m)
	if q.Status != gate.Answered || q.Answer != messages.GateAnswer(messages.Answer{OptionID: "approve"}) || q.By != task.ActorPerson {
		t.Fatalf("the question gate %+v, want answered with the approve option by the person", q)
	}
	if want := []withdrawal{{43, messages.ReasonOtherOption}}; !slices.Equal(od.withdrawn, want) {
		t.Fatalf("withdrawn %+v, want the other option's draft", od.withdrawn)
	}
	err := r.gates.Resolve(ctx, od.gateOf(43).ID, gates.Resolution{Intent: "approve", By: task.ActorPerson})
	if !errors.Is(err, gates.ErrResolved) {
		t.Errorf("approving draft 43's card after: %v, want it already resolved", err)
	}
	if len(od.approvals) != 1 || od.approvals[0].drafted != od.gateOf(42).ID {
		t.Fatalf("approvals %+v, want draft 42's only", od.approvals)
	}
}

// TestApprovingAPinnedDraftThroughItsReviserPicksItsOption approves, through a session the user drives that revised a
// pinned draft, the draft's card as the person would: the question is answered with its option, the other
// option's draft withdrawn; a session the user does not drive leaves the question and both drafts open.
func TestApprovingAPinnedDraftThroughItsReviserPicksItsOption(t *testing.T) {
	ctx := context.Background()
	r, od, m := draftRig(t, "driver", "reviser")
	err := r.gates.Resolve(ctx, od.gateOf(42).ID, gates.Resolution{Intent: "approve", By: task.ActorAgent, Session: "stranger"})
	if !errors.Is(err, gates.ErrRefused) {
		t.Fatalf("an unattended session's approval: %v, want ErrRefused", err)
	}
	if q := r.gateOf(t, m); q.Status != gate.Open || od.gateOf(42).Status != gate.Open || od.gateOf(43).Status != gate.Open || len(od.approvals) != 0 {
		t.Fatalf("question %s, drafts %s and %s, approvals %+v: want all open", q.Status, od.gateOf(42).Status, od.gateOf(43).Status, od.approvals)
	}
	if err := r.gates.Resolve(ctx, od.gateOf(42).ID, gates.Resolution{Intent: "approve", By: task.ActorAgent, Session: "reviser"}); err != nil {
		t.Fatalf("the reviser's approval: %v", err)
	}
	if q := r.gateOf(t, m); q.Status != gate.Answered || od.gateOf(42).Status != gate.Approved {
		t.Fatalf("question %s, draft 42 %s: want the option picked", q.Status, od.gateOf(42).Status)
	}
	if want := []withdrawal{{43, messages.ReasonOtherOption}}; !slices.Equal(od.withdrawn, want) {
		t.Fatalf("withdrawn %+v, want the other option's draft", od.withdrawn)
	}
}

// TestADraftNoQuestionPinsApprovesAlone approves a draft of another turn, which no open question pins, as any
// draft is approved, and leaves the question open.
func TestADraftNoQuestionPinsApprovesAlone(t *testing.T) {
	ctx := context.Background()
	r, od, m := draftRig(t)
	if err := r.gates.Resolve(ctx, od.gateOf(50).ID, gates.Resolution{Intent: "approve", By: task.ActorPerson}); err != nil {
		t.Fatal(err)
	}
	if q := r.gateOf(t, m); q.Status != gate.Open || len(od.withdrawn) != 0 {
		t.Fatalf("question %s, withdrawn %+v: want the question open, nothing withdrawn", q.Status, od.withdrawn)
	}
}
