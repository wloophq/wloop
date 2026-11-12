package messages_test

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/core/gates"
	"github.com/wloophq/wloop/internal/core/messages"
	"github.com/wloophq/wloop/internal/domain/gate"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/turn"
)

// fixedPolicy decides every gate with one mode.
type fixedPolicy gates.Decision

func (p fixedPolicy) Decide(context.Context, gate.Gate) (gates.Decision, error) {
	return gates.Decision(p), nil
}

// reopen replaces draft 42's gate with one the user's policy p decides, as a revision under that policy does.
func reopen(t *testing.T, r *rig, od *optionDrafts, p gates.Policy) gate.Gate {
	t.Helper()
	ctx := context.Background()
	if _, err := r.gateStore.Resolve(ctx, od.gateOf(42).ID, gates.Resolution{Status: gate.Superseded, By: task.ActorWorkflow}); err != nil {
		t.Fatal(err)
	}
	g, err := r.gates.Open(ctx, gate.Gate{Kind: gate.KindDraft, Name: "draft", Unit: r7, Subject: strconv.FormatInt(42, 10),
		Session: "term_1", Sessions: []string{"term_1"}, ApprovedRev: 1}, p)
	if err != nil {
		t.Fatal(err)
	}
	od.mu.Lock()
	od.gateIDs[42] = g.ID
	od.mu.Unlock()
	return g
}

// TestAWindowLeavesAPinnedDraftToItsQuestion (RE6-A): draft 42 waits under the user's one-minute delay while a
// question pins it. A window passing is not a pick: the tick leaves the gate open, and the person's pick of
// comment approves draft 43 alone, whether the window passes before or after the pick.
func TestAWindowLeavesAPinnedDraftToItsQuestion(t *testing.T) {
	ctx := context.Background()
	for _, tickFirst := range []bool{true, false} {
		r, od, m := draftRig(t)
		g := reopen(t, r, od, fixedPolicy{Mode: gate.ModeDelay, Window: time.Minute})
		r.clock.advance(2 * time.Minute)
		if tickFirst {
			if err := r.gates.Tick(ctx, r.clock.Now()); err != nil {
				t.Fatalf("tick: %v", err)
			}
			if got := od.gateOf(42); got.ID != g.ID || got.Status != gate.Open {
				t.Fatalf("the window passed: draft 42's gate %s, want open for its question", got.Status)
			}
		}
		if _, err := r.answer(m, messages.Answer{OptionID: "comment", Source: turn.Cockpit}); err != nil {
			t.Fatalf("picking comment: %v", err)
		}
		if err := r.gates.Tick(ctx, r.clock.Now()); err != nil {
			t.Fatalf("tick after the pick: %v", err)
		}
		if len(od.approvals) != 1 || od.approvals[0].drafted != od.gateOf(43).ID || od.gateOf(42).Status == gate.Approved {
			t.Errorf("tick first %v: approvals %+v, draft 42's gate %s: want draft 43's approval alone", tickFirst, od.approvals, od.gateOf(42).Status)
		}
	}
}

// TestNoRuleReleasesAPinnedDraft: a policy that sends draft 42 on its own, at the gate's open (a revision under a
// send rule) or later, leaves it to the question that pins it.
func TestNoRuleReleasesAPinnedDraft(t *testing.T) {
	ctx := context.Background()
	send := fixedPolicy{Mode: gate.ModeSend, Rule: "send"}
	r, od, _ := draftRig(t)
	if g := reopen(t, r, od, send); g.Status != gate.Open {
		t.Fatalf("a send rule released pinned draft 42 at its gate's open: %s", g.Status)
	}
	err := r.gates.Resolve(ctx, od.gateOf(42).ID, gates.Resolution{By: task.ActorWorkflow, Rule: "send"})
	if !errors.Is(err, gates.ErrRefused) || od.gateOf(42).Status != gate.Open || len(od.approvals) != 0 {
		t.Fatalf("a rule released pinned draft 42: %v, gate %s, approvals %+v", err, od.gateOf(42).Status, od.approvals)
	}
}
