package automation_test

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/core/automation"
	"github.com/wloophq/wloop/internal/core/automation/automationtest"
	"github.com/wloophq/wloop/internal/core/gates"
	"github.com/wloophq/wloop/internal/core/gates/gatestest"
	"github.com/wloophq/wloop/internal/domain/gate"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/workflow"
)

// planProfile declares the one plan gate the engine's gates open.
type planProfile struct{}

var planDef = gates.Def{Name: "plan", Kind: gate.KindPlan, Label: "Run this?", Intents: []gates.Intent{
	{Name: "go", Status: gate.Approved}, {Name: "with", Status: gate.Approved, Answer: true}, {Name: "no", Status: gate.Rejected},
}}

func (planProfile) Gate(name string) (gates.Def, bool) { return planDef, name == planDef.Name }
func (planProfile) FirstOf(k gate.Kind) (gates.Def, bool) {
	return planDef, k == gate.KindPlan
}

// releaser is the engine as its plan gates' consequence, under the loop lock
// a gate's resolution takes.
type releaser struct{ r *rig }

func (l releaser) Release(ctx context.Context, g gate.Gate) error {
	l.r.loop.Lock()
	defer l.r.loop.Unlock()
	return l.r.e.Release(ctx, g)
}

// gated builds the rig's engine with plan gates over an in-memory store.
func gated(r *rig) (*gates.Service, *gatestest.Store) {
	st := gatestest.NewStore(r.clock.Now)
	svc := gates.New(gates.Deps{Store: st, Reader: st, Profile: planProfile{}, Clock: r.clock, Releaser: releaser{r}})
	r.e.Gates = gateAccess{svc, st}
	return svc, st
}

type gateAccess struct {
	*gates.Service
	r gates.Reader
}

func (g gateAccess) Where(ctx context.Context, q gates.Query) ([]gate.Gate, error) {
	return g.r.Where(ctx, q)
}

func gateOf(t *testing.T, st *gatestest.Store, subject string, statuses ...gate.Status) (gate.Gate, bool) {
	t.Helper()
	all, err := st.Where(context.Background(), gates.Query{Kind: gate.KindPlan, Statuses: statuses})
	if err != nil {
		t.Fatal(err)
	}
	i := slices.IndexFunc(all, func(g gate.Gate) bool { return g.Subject == subject })
	if i < 0 {
		return gate.Gate{}, false
	}
	return all[i], true
}

var closedOrOpen = []gate.Status{gate.Open, gate.Approved, gate.Rejected, gate.Answered, gate.Dismissed, gate.Superseded}

func TestAPendingRoundIsAPlanGate(t *testing.T) {
	ctx := context.Background()
	waits := sweepWF("waits", idle, "session.close")
	waits.Approval = workflow.Approval{Mode: workflow.ApprovalWait}
	observed := sweepWF("observed", idle, "session.mark_closed")
	observed.Observe = true
	onA := automationtest.MatchFunc(func(v workflow.Vars) bool { return v.Facts["session.fingerprint"] == "a" }, "session.fingerprint")
	r := newRig(t, windowed(sweepWF("delayed", onA, "turn.start"), 5*time.Minute), waits, observed)
	svc, st := gated(r)
	r.put(s1, "a")
	r.put(s2, "b")
	r.sweep()

	delayed := r.mustItem("delayed", s1)
	g, ok := gateOf(t, st, delayed.ID)
	if !ok || g.Status != gate.Open || g.Unit != s1 || g.Name != "plan" || !g.Deadline.Equal(t0.Add(5*time.Minute)) || g.Default != "go" {
		t.Fatalf("the delayed round's gate = %+v %v, want open on %s until its window ends with default go", g, ok, s1)
	}
	held := r.mustItem("waits", s1)
	if w, ok := gateOf(t, st, held.ID); !ok || !w.Deadline.IsZero() || w.Default != "" {
		t.Fatalf("the waiting item's gate = %+v %v, want open with no deadline", w, ok)
	}
	if o, ok := gateOf(t, st, r.mustItem("observed", s1).ID, closedOrOpen...); ok {
		t.Fatalf("an observed item opened a gate: %+v", o)
	}
	r.sweep()
	if open, _ := st.Where(ctx, gates.Query{Kind: gate.KindPlan}); len(open) != 3 {
		t.Fatalf("a second sweep opened gates again: %+v", open)
	}

	if err := r.e.DueRun(ctx); err != nil {
		t.Fatal(err)
	}
	r.clock.Advance(5 * time.Minute)
	if err := r.e.DueRun(ctx); err != nil {
		t.Fatal(err)
	}
	if got := r.ran("turn.start"); len(got) != 0 {
		t.Fatalf("a due run ran a gated window: %v", got)
	}
	if err := svc.Tick(ctx, r.clock.Now()); err != nil {
		t.Fatal(err)
	}
	if got := r.ran("turn.start"); !slices.Equal(got, []unit.Unit{s1}) {
		t.Fatalf("the window's end ran %v, want %s", got, s1)
	}
	if g, _ := gateOf(t, st, delayed.ID, closedOrOpen...); g.Status != gate.Approved || g.Rule != gates.RuleWindow || g.By != task.ActorWorkflow {
		t.Fatalf("the delayed round's gate = %+v, want approved by its window", g)
	}

	if err := svc.Resolve(ctx, mustGate(t, st, held.ID).ID, gates.Resolution{Intent: "no", By: task.ActorPerson}); err != nil {
		t.Fatal(err)
	}
	if _, ok := r.item("waits", s1); ok || len(r.ran("session.close")) != 0 {
		t.Fatalf("skipping the gate left %+v, ran %v", r.items(), r.ran("session.close"))
	}
	if run, ok, _ := r.ledger.Run(ctx, held.Key); !ok || run.Status != workflow.RunDismissed {
		t.Fatalf("the skipped item's run = %+v %v, want dismissed", run, ok)
	}

	other := r.mustItem("waits", s2)
	if err := svc.Resolve(ctx, mustGate(t, st, other.ID).ID, gates.Resolution{Intent: "with", Answer: "look at the tests", By: task.ActorPerson}); err != nil {
		t.Fatal(err)
	}
	if got := r.ran("session.close"); !slices.Equal(got, []unit.Unit{s2}) {
		t.Fatalf("the guided approval ran %v, want %s", got, s2)
	}

	r.put(s3, "c")
	r.sweep()
	gone := r.mustItem("waits", s3)
	mustGate(t, st, gone.ID)
	r.put(s3, "c", "session.idle", false)
	r.sweep()
	if g, _ := gateOf(t, st, gone.ID, closedOrOpen...); g.Status != gate.Superseded || g.By != task.ActorWorkflow {
		t.Fatalf("the gate of an item that left the plan = %+v, want superseded", g)
	}
}

func mustGate(t *testing.T, st *gatestest.Store, subject string) gate.Gate {
	t.Helper()
	g, ok := gateOf(t, st, subject)
	if !ok {
		t.Fatalf("no open gate on %s", subject)
	}
	return g
}

var _ automation.Gates = gateAccess{}
