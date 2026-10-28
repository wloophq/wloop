package automation_test

import (
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/core/automation"
	"github.com/wloophq/wloop/internal/core/automation/automationtest"
	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/workflow"
)

var never = automationtest.MatchFunc(func(workflow.Vars) bool { return false })

// withdrawWF proposes on sessions and withdraws a draft whose
// draft.items_assigned is positive or whose session is not idle.
func withdrawWF(name, reason string) workflow.Workflow {
	w := sweepWF(name, never, "notify.self")
	w.Withdraw = automationtest.MatchFunc(func(v workflow.Vars) bool {
		return v.Facts["draft.items_assigned"].(int) > 0 || v.Facts["session.idle"] != true
	}, "draft.items_assigned", "session.idle")
	w.WithdrawReason = reason
	return w
}

func withWithdrawals(r *rig) *automationtest.Withdrawals {
	ws := automationtest.NewWithdrawals()
	r.e.Withdrawals = ws
	return ws
}

func owned(id int64, u unit.Unit, n int) automation.ProposedDraft {
	return automation.ProposedDraft{ID: id, Subject: u, Facts: workflow.Facts{"draft.items_assigned": n}}
}

func TestTheSweepWithdrawsADraftByItsWorkflowsWithdrawWhen(t *testing.T) {
	r := newRig(t, withdrawWF("ask", draft.WithdrawAssigned), withdrawWF("nudge", ""))
	ws := withWithdrawals(r)
	r.put(s1, "a")
	r.put(s2, "b")
	r.put(s3, "c", "session.idle", false)
	ws.Put("ask", owned(1, s1, 1), owned(2, s2, 0))
	ws.Put("nudge", owned(3, s3, 0))
	r.sweep()
	for id, want := range map[int64]string{1: draft.WithdrawAssigned, 3: draft.WithdrawMoot} {
		if got, ok := ws.Withdrawn(id); !ok || got != want {
			t.Errorf("draft %d: withdrawn %v %q, want %q", id, ok, got, want)
		}
	}
	if _, ok := ws.Withdrawn(2); ok {
		t.Error("draft 2 matches no withdraw when and was withdrawn")
	}

	gone := newRig(t, withdrawWF("ask", draft.WithdrawAssigned))
	gws := withWithdrawals(gone)
	gws.Put("ask", owned(4, s1, 0))
	gone.sweep()
	if got, ok := gws.Withdrawn(4); !ok || got != draft.WithdrawMoot {
		t.Errorf("the draft of a gone subject: %v %q", ok, got)
	}

	observe := withdrawWF("ask", draft.WithdrawAssigned)
	observe.Observe = true
	o := newRig(t, observe)
	ows := withWithdrawals(o)
	o.put(s1, "a")
	ows.Put("ask", owned(5, s1, 1))
	o.sweep()
	if _, ok := ows.Withdrawn(5); ok {
		t.Error("an observing workflow withdrew a draft")
	}
}

func TestNoDraftOfADisabledPluginIsWithdrawn(t *testing.T) {
	r := newRig(t, withdrawWF("ask", draft.WithdrawAssigned))
	ws := withWithdrawals(r)
	r.e.PluginEnabled = func(p unit.ProviderID) bool { return p != unit.Session }
	r.put(s1, "a")
	ws.Put("ask", owned(1, s1, 1), owned(2, s2, 0))
	ws.Put("unloaded", owned(3, s1, 0))
	r.sweep()
	for _, id := range []int64{1, 2, 3} {
		if got, ok := ws.Withdrawn(id); ok {
			t.Errorf("draft %d of a disabled plugin withdrawn: %q", id, got)
		}
	}
}

func TestADraftOfAnUnloadedWorkflowIsWithdrawnMoot(t *testing.T) {
	r := newRig(t, withdrawWF("ask", draft.WithdrawAssigned))
	ws := withWithdrawals(r)
	r.put(s1, "a")
	ws.Put("renamed-away", owned(1, s1, 0))
	ws.Put("ask", owned(2, s1, 0))
	r.sweep()
	if got, ok := ws.Withdrawn(1); !ok || got != draft.WithdrawMoot {
		t.Fatalf("the draft of an unloaded workflow: %v %q", ok, got)
	}
	if _, ok := ws.Withdrawn(2); ok {
		t.Fatal("a loaded workflow's draft that matches nothing was withdrawn")
	}
}

func TestPlanningWithdrawsNothing(t *testing.T) {
	r := newRig(t, withdrawWF("ask", draft.WithdrawAssigned))
	ws := withWithdrawals(r)
	r.put(s1, "a")
	ws.Put("ask", owned(1, s1, 1))
	ws.Put("unloaded", owned(2, s1, 0))
	r.evaluate()
	for _, id := range []int64{1, 2} {
		if _, ok := ws.Withdrawn(id); ok {
			t.Errorf("planning withdrew draft %d", id)
		}
	}
}

func TestWithdrawWhenReadsTheFactsScopedToItsWorkflow(t *testing.T) {
	w := withdrawWF("ask", draft.WithdrawAssigned)
	w.Withdraw = automationtest.MatchFunc(func(v workflow.Vars) bool {
		at, _ := v.Facts["session.settled"].(time.Time)
		return !at.IsZero()
	}, "session.settled")
	r := newRig(t, w)
	ws := withWithdrawals(r)
	su := scopedUnits{Units: r.units, settled: map[string]time.Time{"ask": t0}}
	r.e.Units, r.e.Keep = su, su
	r.put(s1, "a")
	ws.Put("ask", owned(1, s1, 0))
	r.sweep()
	if got, ok := ws.Withdrawn(1); !ok || got != draft.WithdrawAssigned {
		t.Fatalf("withdraw when read the unscoped facts: %v %q", ok, got)
	}
}

func TestADraftOfAWorkflowThatFailsToLoadStays(t *testing.T) {
	r := newRig(t, withdrawWF("ask", draft.WithdrawAssigned))
	ws := withWithdrawals(r)
	r.e.Broken = func() []string { return []string{"my-followups"} }
	r.put(s1, "a")
	ws.Put("my-followups", owned(1, s1, 0))
	ws.Put("removed", owned(2, s1, 0))
	r.sweep()
	if got, ok := ws.Withdrawn(1); ok {
		t.Fatalf("the draft of a workflow that failed to load was withdrawn: %q", got)
	}
	if got, ok := ws.Withdrawn(2); !ok || got != draft.WithdrawMoot {
		t.Fatalf("the draft of a removed workflow: %v %q", ok, got)
	}
}
