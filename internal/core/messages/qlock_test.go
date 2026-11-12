package messages_test

import (
	"context"
	"errors"
	"slices"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/wloophq/wloop/internal/core/gates"
	"github.com/wloophq/wloop/internal/core/messages"
	"github.com/wloophq/wloop/internal/domain/gate"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/turn"
)

// TestARevisedPinnedDraftIsDecidedByItsQuestionOnly (RE2-A): the agent revises draft 42 to rev 2 after its question
// pinned rev 1. Approving draft 42's card (outbox approve, the task verb) is refused, since the open question is
// the decision; picking the question's comment option then approves draft 43 alone. One question, one verdict.
func TestARevisedPinnedDraftIsDecidedByItsQuestionOnly(t *testing.T) {
	ctx := context.Background()
	r, od, m := draftRig(t)
	od.put(heldDraft(42, r7, 1, 2), "term_1")
	if err := r.gates.Resolve(ctx, od.gateOf(42).ID, gates.Resolution{Intent: "approve", By: task.ActorPerson}); !errors.Is(err, gates.ErrRefused) {
		t.Fatalf("approving draft 42 at rev 2 beside its open question: %v, want ErrRefused", err)
	}
	if _, err := r.answer(m, messages.Answer{OptionID: "comment", Source: turn.Cockpit}); err != nil {
		t.Fatalf("picking comment: %v", err)
	}
	if len(od.approvals) != 1 || od.approvals[0].drafted != od.gateOf(43).ID || od.gateOf(42).Status != gate.Superseded && od.gateOf(42).Status != gate.Open {
		t.Errorf("one question approved %+v; draft 42's gate %s: want draft 43's approval only", od.approvals, od.gateOf(42).Status)
	}
}

// interleaved is the outbox's consequence of an approved draft gate that, on the first approval, lets a
// concurrent answer to the question run before the approval's own ApproveOption answers the question: the
// person's comment pick arriving through the question's route, which takes no draft lock, while their
// approval of draft 42's card is in flight.
type interleaved struct {
	*optionDrafts
	fired atomic.Bool
	then  func()
}

func (i *interleaved) Publish(ctx context.Context, g gate.Gate) error {
	err := i.optionDrafts.Publish(ctx, g)
	if i.fired.CompareAndSwap(false, true) {
		i.then()
	}
	return err
}

// TestADecisionInFlightRefusesAnotherOnItsQuestion (RE2-B): two decisions on one question at once, draft 42's card
// approved and the comment option picked while the approval runs. The question is claimed by the first, so the
// pick is refused: draft 42 alone is approved, the question answered with its option and draft 43 withdrawn.
func TestADecisionInFlightRefusesAnotherOnItsQuestion(t *testing.T) {
	ctx := context.Background()
	r, od, m := draftRig(t)
	hook := &interleaved{optionDrafts: od}
	hook.then = func() {
		if _, err := r.answer(m, messages.Answer{OptionID: "comment", Source: turn.Cockpit}); err != nil {
			t.Logf("the concurrent comment pick: %v", err)
		}
	}
	r.gates = gates.New(gates.Deps{Store: r.gateStore, Reader: r.gateStore, Profile: profileGates, Tasks: gateTasks{r.taskStore, r.tasks}, Clock: r.clock,
		Deliverer: r.svc, Decider: r.svc, Publisher: hook, Options: r.svc, Log: t.Logf, Sessions: drivenSessions(nil)})
	r.svc.Gates = serviceGates{r.gates, r.gateStore}
	if err := r.gates.Resolve(ctx, od.gateOf(42).ID, gates.Resolution{Intent: "approve", By: task.ActorPerson}); err != nil {
		t.Fatalf("approving draft 42's card: %v", err)
	}
	q := r.gateOf(t, m)
	if len(od.approvals) != 1 || od.approvals[0].drafted != od.gateOf(42).ID || q.Answer != messages.GateAnswer(messages.Answer{OptionID: "approve"}) {
		t.Errorf("approvals %+v, question %s %q: want draft 42's alone and the question answered with its option", od.approvals, q.Status, q.Answer)
	}
	if !slices.Contains(od.withdrawn, withdrawal{43, messages.ReasonOtherOption}) {
		t.Errorf("withdrawn %+v, want draft 43", od.withdrawn)
	}
}

// TestACardApprovalAndAnOptionPickRaceToOneVerdict runs draft 42's card approval and the comment pick on two
// goroutines at once: whichever wins, one draft is approved and the question names it.
func TestACardApprovalAndAnOptionPickRaceToOneVerdict(t *testing.T) {
	ctx := context.Background()
	r, od, m := draftRig(t)
	var wg sync.WaitGroup
	wg.Go(func() {
		_ = r.gates.Resolve(ctx, od.gateOf(42).ID, gates.Resolution{Intent: "approve", By: task.ActorPerson})
	})
	wg.Go(func() { _, _ = r.answer(m, messages.Answer{OptionID: "comment", Source: turn.Cockpit}) })
	wg.Wait()
	od.mu.Lock()
	approvals := slices.Clone(od.approvals)
	od.mu.Unlock()
	if len(approvals) != 1 {
		t.Fatalf("approvals %+v, want one", approvals)
	}
	want := map[int64]string{od.gateOf(42).ID: "approve", od.gateOf(43).ID: "comment"}[approvals[0].drafted]
	if q := r.gateOf(t, m); q.Status != gate.Answered || q.Answer != messages.GateAnswer(messages.Answer{OptionID: want}) {
		t.Errorf("question %s %q, want answered %q, the approved draft's option", q.Status, q.Answer, want)
	}
}

// unopened answers that a question pins every draft but has no gate yet, as when the message is stored and its
// gate still opening.
type unopened struct{ *messages.Service }

func (u unopened) Pinning(_ context.Context, drafted gate.Gate) (gate.Gate, string, int64, bool, error) {
	return gate.Gate{}, messages.GateAnswer(messages.Answer{OptionID: "approve"}), drafted.ApprovedRev, true, nil
}

// TestAPinnedDraftWaitsForItsQuestionToOpen refuses a draft's approval while the question that pins it is stored
// but its gate is not open yet: the question, once open, is the decision.
func TestAPinnedDraftWaitsForItsQuestionToOpen(t *testing.T) {
	ctx := context.Background()
	r, od, _ := draftRig(t)
	r.gates = gates.New(gates.Deps{Store: r.gateStore, Reader: r.gateStore, Profile: profileGates, Tasks: gateTasks{r.taskStore, r.tasks}, Clock: r.clock,
		Deliverer: r.svc, Decider: r.svc, Publisher: od, Options: unopened{r.svc}, Log: t.Logf, Sessions: drivenSessions(nil)})
	if err := r.gates.Resolve(ctx, od.gateOf(42).ID, gates.Resolution{Intent: "approve", By: task.ActorPerson}); !errors.Is(err, gates.ErrRefused) {
		t.Fatalf("approving a draft its unopened question pins: %v, want ErrRefused", err)
	}
	if len(od.approvals) != 0 || od.gateOf(42).Status != gate.Open {
		t.Fatalf("approvals %+v, draft gate %s: want none, still open", od.approvals, od.gateOf(42).Status)
	}
}
