package source_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/core/automation"
	"github.com/wloophq/wloop/internal/core/automation/automationtest"
	"github.com/wloophq/wloop/internal/core/gates"
	"github.com/wloophq/wloop/internal/domain/gate"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/workflow"
	"github.com/wloophq/wloop/pkg/plugin"
	src "github.com/wloophq/wloop/pkg/plugin/source"
	"github.com/wloophq/wloop/pkg/plugin/source/sourcetest"
)

type planOnly struct{}

var planDef = gates.Def{Name: "plan", Kind: gate.KindPlan, Intents: []gates.Intent{{Name: "run_now", Status: gate.Approved}, {Name: "skip", Status: gate.Rejected}}}

func (planOnly) Gate(name string) (gates.Def, bool)    { return planDef, name == planDef.Name }
func (planOnly) FirstOf(k gate.Kind) (gates.Def, bool) { return planDef, k == gate.KindPlan }

type engineGates struct {
	*gates.Service
	r gates.Reader
}

func (g engineGates) Where(ctx context.Context, q gates.Query) ([]gate.Gate, error) {
	return g.r.Where(ctx, q)
}

func TestRoundStatePendingCarriesThePlanGates(t *testing.T) {
	t.Parallel()
	b, db := brokerOver(t)
	ctx := context.Background()
	t0 := time.Date(2026, 10, 28, 9, 0, 0, 0, time.UTC)
	clock := automationtest.NewClock(t0)
	t1 := unit.Unit{Provider: "acme", Key: "t1"}
	units := automationtest.NewUnits()
	units.Put(t1, workflow.Facts{"acme.fingerprint": "f1"})
	steps := automationtest.NewSteps(map[string]workflow.Reach{"turn.start": workflow.ReachLocal})
	st := db.Gates(clock.Now)
	eng := &automation.Engine{
		Workflows: func() []workflow.Workflow {
			return []workflow.Workflow{{Name: "acme-round", Provider: "acme", Slot: "acme", Trigger: workflow.Trigger{Kind: workflow.TriggerSweep},
				Steps: []workflow.Step{steps.Step("turn.start")}, Approval: workflow.Approval{Mode: workflow.ApprovalDelay, Window: 5 * time.Minute}}}
		},
		Units: units, Steps: steps, Ledger: db.Ledger(), Events: db.Events(), Clock: clock, Codecs: unit.NewRegistry(),
		Gates: engineGates{gates.New(gates.Deps{Store: st, Reader: st, Profile: planOnly{}, Clock: clock}), st},
	}
	if _, _, err := eng.Sweep(ctx, &sync.Mutex{}); err != nil {
		t.Fatal(err)
	}
	b.Pending = func(ctx context.Context, u unit.Unit) ([]src.PendingRound, error) {
		ws, err := eng.Waiting(ctx, &u)
		out := make([]src.PendingRound, len(ws))
		for i, w := range ws {
			out[i] = src.PendingRound{Intent: w.Item.Workflow, Reason: w.Item.Reason, Since: w.Gate.OpenedAt, Deadline: w.Gate.Deadline}
		}
		return out, err
	}
	stub := sourcetest.NewStub()
	sourcetest.Launch(t, "acme", stub, b.Host())
	got, err := stub.Host().Reader.RoundState(ctx, plugin.Unit{Provider: "acme", Key: "t1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Pending) != 1 || got.Pending[0].Intent != "acme-round" || !got.Pending[0].Since.Equal(t0) || !got.Pending[0].Deadline.Equal(t0.Add(5*time.Minute)) {
		t.Fatalf("pending %+v, want the plan gate's round until its window ends", got.Pending)
	}
	open, _ := st.Where(ctx, gates.Query{Kind: gate.KindPlan, Unit: t1})
	if err := gates.New(gates.Deps{Store: st, Reader: st, Profile: planOnly{}, Clock: clock}).Supersede(ctx, open[0].ID, "workflow"); err != nil {
		t.Fatal(err)
	}
	if got, err := stub.Host().Reader.RoundState(ctx, plugin.Unit{Provider: "acme", Key: "t1"}); err != nil || len(got.Pending) != 0 {
		t.Fatalf("pending with its gate closed %+v %v", got.Pending, err)
	}
}
