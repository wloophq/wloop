package automation_test

import (
	"context"
	"errors"
	"sync"
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

func TestAWithdrawRuleWithdrawsWithItsOwnReasonBeforeTheWhen(t *testing.T) {
	w := withdrawWF("nudge", draft.WithdrawMoot)
	w.WithdrawRules = []workflow.WithdrawRule{{When: automationtest.MatchFunc(func(v workflow.Vars) bool {
		return v.Facts["draft.people_outside"] == 1
	}, "draft.people_outside"), Reason: draft.WithdrawOutside}}
	r := newRig(t, w)
	ws := withWithdrawals(r)
	r.put(s1, "a")
	r.put(s2, "b", "session.idle", false)
	outside := owned(1, s1, 0)
	outside.Facts["draft.people_outside"] = 1
	both := owned(2, s2, 0)
	both.Facts["draft.people_outside"] = 1
	moot := owned(3, s2, 0)
	moot.Facts["draft.people_outside"] = 0
	ws.Put("nudge", outside, both, moot)
	r.sweep()
	for id, want := range map[int64]string{1: draft.WithdrawOutside, 2: draft.WithdrawOutside, 3: draft.WithdrawMoot} {
		if got, ok := ws.Withdrawn(id); !ok || got != want {
			t.Errorf("draft %d: withdrawn %v %q, want %q", id, ok, got, want)
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

type passKey struct{}

// passingUnits number the passes the engine prepares and keep, of each
// facts read, the pass it was made in.
type passingUnits struct {
	*automationtest.Units
	passes int
	reads  map[unit.Unit][]int
}

func (p *passingUnits) Prepare(ctx context.Context, fn func(context.Context) error) error {
	p.passes++
	return fn(context.WithValue(ctx, passKey{}, p.passes))
}

func (p *passingUnits) Facts(ctx context.Context, u unit.Unit) (workflow.Facts, error) {
	n, _ := ctx.Value(passKey{}).(int)
	p.reads[u] = append(p.reads[u], n)
	return p.Units.Facts(ctx, u)
}

// withdrawnIn records the pass a withdrawal is written after.
type withdrawnIn struct {
	*automationtest.Withdrawals
	p     *passingUnits
	after map[int64]int
}

func (w withdrawnIn) Withdraw(ctx context.Context, id int64, reason string) error {
	if _, in := ctx.Value(passKey{}).(int); in {
		return errors.New("a withdrawal written inside a pass")
	}
	w.after[id] = w.p.passes
	return w.Withdrawals.Withdraw(ctx, id, reason)
}

func TestTheSweepReadsTheDraftsAfterAWithdrawalInANewPass(t *testing.T) {
	r := newRig(t, withdrawWF("ask", draft.WithdrawAssigned))
	pu := &passingUnits{Units: r.units, reads: map[unit.Unit][]int{}}
	r.e.Units, r.e.Keep = pu, pu
	ws := withdrawnIn{Withdrawals: automationtest.NewWithdrawals(), p: pu, after: map[int64]int{}}
	r.e.Withdrawals = ws
	r.put(s1, "a")
	r.put(s2, "b")
	r.put(s3, "c")
	ws.Put("ask", owned(1, s1, 1), owned(2, s2, 0), owned(3, s3, 0))
	r.sweep()
	if _, ok := ws.Withdrawn(1); !ok {
		t.Fatal("draft 1 was not withdrawn")
	}
	last := func(u unit.Unit) int { return pu.reads[u][len(pu.reads[u])-1] }
	if w := ws.after[1]; last(s1) != w || last(s2) <= w || last(s3) != last(s2) {
		t.Errorf("draft 1 withdrawn after pass %d; its subject read last in pass %d, the next drafts' in %d and %d",
			w, last(s1), last(s2), last(s3))
	}
}

// movingWithdrawals are Withdrawals whose drafts' facts read the store as it
// is: withdrawing draft from assigns the items of draft to.
type movingWithdrawals struct {
	*automationtest.Withdrawals
	from, to int64
}

func (w movingWithdrawals) Proposed(ctx context.Context, wf string) ([]automation.ProposedDraft, error) {
	ds, err := w.Withdrawals.Proposed(ctx, wf)
	if _, gone := w.Withdrawn(w.from); gone {
		for i, d := range ds {
			if d.ID == w.to {
				ds[i].Facts = workflow.Facts{"draft.items_assigned": 1}
			}
		}
	}
	return ds, err
}

func TestADraftAfterAWithdrawalIsJudgedOnItsOwnFactsAsTheWithdrawalLeftThem(t *testing.T) {
	r := newRig(t, withdrawWF("ask", draft.WithdrawAssigned))
	ws := movingWithdrawals{Withdrawals: automationtest.NewWithdrawals(), from: 1, to: 2}
	r.e.Withdrawals = ws
	r.put(s1, "a")
	r.put(s2, "b")
	ws.Put("ask", owned(1, s1, 1), owned(2, s2, 0))
	r.sweep()
	if _, ok := ws.Withdrawn(2); !ok {
		t.Fatal("draft 2 was judged on the facts it had before draft 1 was withdrawn")
	}
}

// idleKeep is a Keep whose Prepare never runs the pass.
type idleKeep struct{ *automationtest.Units }

func (idleKeep) Prepare(context.Context, func(context.Context) error) error { return nil }

func TestASweepWhosePassNeverRunsFails(t *testing.T) {
	r := newRig(t, withdrawWF("ask", draft.WithdrawAssigned))
	r.e.Keep = idleKeep{r.units}
	if _, _, err := r.e.Sweep(t.Context(), &sync.Mutex{}); err == nil {
		t.Fatal("a sweep whose pass never ran succeeded")
	}
}

func TestADraftOfADisabledWorkflowStays(t *testing.T) {
	r := newRig(t, withdrawWF("ask", draft.WithdrawAssigned))
	ws := withWithdrawals(r)
	r.e.Disabled = func() []string { return []string{"nudge"} }
	r.put(s1, "a")
	ws.Put("nudge", owned(1, s1, 0))
	r.sweep()
	if got, ok := ws.Withdrawn(1); ok {
		t.Fatalf("the draft of a disabled workflow was withdrawn: %q", got)
	}
}
