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
	return reopenOf(t, r, od, 42, p)
}

func reopenOf(t *testing.T, r *rig, od *optionDrafts, id int64, p gates.Policy) gate.Gate {
	t.Helper()
	ctx := context.Background()
	if _, err := r.gateStore.Resolve(ctx, od.gateOf(id).ID, gates.Resolution{Status: gate.Superseded, By: task.ActorWorkflow}); err != nil {
		t.Fatal(err)
	}
	g, err := r.gates.Open(ctx, gate.Gate{Kind: gate.KindDraft, Name: "draft", Unit: r7, Subject: strconv.FormatInt(id, 10),
		Session: "term_1", Sessions: []string{"term_1"}, ApprovedRev: 1}, p)
	if err != nil {
		t.Fatal(err)
	}
	od.mu.Lock()
	od.gateIDs[id] = g.ID
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

// TestADeclinedQuestionLeavesItsDelayedPinsToThePerson (RE7-A): drafts 42 and 43 wait under the user's delay window,
// which passes while the question pins them. The person dismisses the question, or answers it in free text: no
// window releases either draft afterwards. Each keeps waiting, now for the person alone, with no countdown.
func TestADeclinedQuestionLeavesItsDelayedPinsToThePerson(t *testing.T) {
	for _, how := range []string{"dismiss", "free text"} {
		t.Run(how, func(t *testing.T) {
			ctx := context.Background()
			r, od, m := draftRig(t)
			delay := fixedPolicy{Mode: gate.ModeDelay, Window: time.Minute}
			reopen(t, r, od, delay)
			reopenOf(t, r, od, 43, delay)
			r.clock.advance(2 * time.Minute)
			if err := r.gates.Tick(ctx, r.clock.Now()); err != nil {
				t.Fatalf("tick: %v", err)
			}
			var err error
			if how == "dismiss" {
				_, err = r.resolve(m, "dismiss", messages.Answer{Source: turn.Cockpit})
			} else {
				_, err = r.answer(m, messages.Answer{Text: "neither, ask the author first", Source: turn.Cockpit})
			}
			if err != nil {
				t.Fatalf("%s: %v", how, err)
			}
			r.clock.advance(time.Minute)
			if err := r.gates.Tick(ctx, r.clock.Now()); err != nil {
				t.Fatalf("tick: %v", err)
			}
			if len(od.approvals) != 0 {
				t.Fatalf("a window released a draft the person declined to pick: %+v", od.approvals)
			}
			for _, id := range []int64{42, 43} {
				if g := od.gateOf(id); g.Status != gate.Open || !g.Deadline.IsZero() || g.Default != "" {
					t.Errorf("draft %d's gate %s, deadline %v, default %q: want open for the person, with no window", id, g.Status, g.Deadline, g.Default)
				}
			}
			if err := r.gates.Resolve(ctx, od.gateOf(42).ID, gates.Resolution{Intent: "approve", By: task.ActorPerson}); err != nil || len(od.approvals) != 1 {
				t.Fatalf("the person's approval of draft 42: %v, approvals %+v", err, od.approvals)
			}
		})
	}
}
