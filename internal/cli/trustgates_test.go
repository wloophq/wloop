package cli

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/core/gates"
	"github.com/wloophq/wloop/internal/core/gates/gatestest"
	coretasks "github.com/wloophq/wloop/internal/core/tasks"
	"github.com/wloophq/wloop/internal/core/tasks/taskstest"
	"github.com/wloophq/wloop/internal/daemon"
	"github.com/wloophq/wloop/internal/domain/gate"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/domain/round"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
)

// releasesAll is an approval that would release any gate at once.
type releasesAll struct{}

func (releasesAll) Decide(context.Context, gate.Gate) (gates.Decision, error) {
	return gates.Decision{Mode: gate.ModeRun, Rule: "everything"}, nil
}

func TestAStartBlockWaitsOnItsTrustGateUntilItLifts(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r := newBlockRig(t, trustBlockKey)
	e := r.sc.env
	st := gatestest.NewStore(e.Now)
	e.gates = gates.New(gates.Deps{Store: st, Reader: st, Profile: profileGates{e.loadedIntents}, Policy: releasesAll{}, Clock: clockFunc(e.Now)})
	e.gateReader = st
	for range 2 {
		if err := e.syncTrustGates(ctx); err != nil {
			t.Fatal(err)
		}
	}
	open, _ := st.Where(ctx, gates.Query{Kind: gate.KindTrust})
	if len(open) != 1 || open[0].Unit != r.ref.Unit() || open[0].Subject != trustBlockKey || open[0].Name != "trust" {
		t.Fatalf("trust gates %+v, want one open on %s naming its block", open, r.ref)
	}
	r.rec.events = append(r.rec.events, dinv.Event{PR: r.ref, Kind: dinv.KindRoundUnblocked, At: testNow, Data: "the agent home trusts its folder"})
	if err := e.syncTrustGates(ctx); err != nil {
		t.Fatal(err)
	}
	g, _ := st.Get(ctx, open[0].ID)
	if g.Status != gate.Superseded || g.By != task.ActorWorkflow {
		t.Fatalf("the gate of a lifted block = %+v, want superseded", g)
	}
	r.block(trustBlockKey, testNow.Add(time.Minute))
	if err := e.syncTrustGates(ctx); err != nil {
		t.Fatal(err)
	}
	if open, _ := st.Where(ctx, gates.Query{Kind: gate.KindTrust}); len(open) != 1 {
		t.Fatalf("a new block's gates %+v", open)
	}
}

func TestTrustRunOfAReplacedBlockTrustsNothing(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r := newBlockRig(t, trustBlockKey)
	e := r.sc.env
	st := gatestest.NewStore(e.Now)
	e.gates = gates.New(gates.Deps{Store: st, Reader: st, Profile: profileGates{e.loadedIntents}, Clock: clockFunc(e.Now)})
	e.gateReader = st
	if err := e.syncTrustGates(ctx); err != nil {
		t.Fatal(err)
	}
	first, _ := st.Where(ctx, gates.Query{Kind: gate.KindTrust})
	const replaced = "2026-10-21T15:00:00Z\ntrust\n/w/other\n/w/other\n\n\nfirst\n\n"
	r.block(replaced, testNow.Add(-time.Minute))
	if err := e.syncTrustGates(ctx); err != nil {
		t.Fatal(err)
	}
	if g, _ := st.Get(ctx, first[0].ID); g.Status != gate.Superseded {
		t.Fatalf("the gate of the replaced block = %+v, want superseded", g)
	}
	open, _ := st.Where(ctx, gates.Query{Kind: gate.KindTrust})
	if len(open) != 1 || open[0].Subject != replaced {
		t.Fatalf("open trust gates %+v, want one on the new block", open)
	}
	a := envActions{e: e}
	before := len(r.rec.events)
	for name, act := range map[string]func(context.Context, unit.Unit, daemon.Call) error{"trust_run": a.TrustAndRun, "unblock": a.Unblock} {
		err := act(ctx, r.ref.Unit(), daemon.Call{Block: trustBlockKey, Out: io.Discard})
		var refused daemon.Refusal
		if !errors.As(err, &refused) {
			t.Fatalf("%s on the replaced block = %v, want a refusal", name, err)
		}
	}
	if len(r.rec.events) != before {
		t.Fatalf("acting on the replaced block recorded %v", r.rec.events[before:])
	}
	if key, ok, err := a.StartBlockKey(ctx, r.ref.Unit()); err != nil || !ok || key != replaced {
		t.Fatalf("the current block %q %v %v", key, ok, err)
	}
}

func TestAHeldGateOfAnEndedPRIsSuperseded(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r := newBlockRig(t, trustBlockKey)
	e := r.sc.env
	store := taskstest.New()
	e.tasks = coretasks.New(coretasks.Deps{Store: store, States: taskstest.BundledStates, Clock: clockFunc(e.Now)})
	merged := task.Task{ID: "t-00000000000000e1", State: "done", Category: task.CategoryDone, Title: "Review acme/api#1", Units: []unit.Unit{r.ref.Unit()},
		Origin: task.OriginDerived, Created: testNow, Updated: testNow}
	open := task.Task{ID: "t-00000000000000e2", State: "next", Category: task.CategoryReady, Title: "Review acme/api#2", Units: []unit.Unit{{Provider: unit.GitHub, Key: "acme/api#2"}},
		Origin: task.OriginDerived, Created: testNow, Updated: testNow}
	if err := store.Apply(ctx, []task.Change{{Op: task.OpCreate, Task: merged}, {Op: task.OpCreate, Task: open}}); err != nil {
		t.Fatal(err)
	}
	st := gatestest.NewStore(e.Now)
	e.gates = gates.New(gates.Deps{Store: st, Reader: st, Profile: profileGates{e.loadedIntents}, Clock: clockFunc(e.Now)})
	e.gateReader = st
	ended, err := e.gates.Open(ctx, gate.Gate{Kind: gate.KindPlan, Unit: merged.Units[0], Task: merged.ID, Subject: round.HeldSubject(e.Now())}, waitsForYou{})
	if err != nil {
		t.Fatal(err)
	}
	live, err := e.gates.Open(ctx, gate.Gate{Kind: gate.KindPlan, Unit: open.Units[0], Task: open.ID, Subject: round.HeldSubject(e.Now())}, waitsForYou{})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.supersedeHeldGates(ctx); err != nil {
		t.Fatal(err)
	}
	if g, _ := st.Get(ctx, ended.ID); g.Status != gate.Superseded {
		t.Errorf("the held gate of a merged pull request's task is %s", g.Status)
	}
	if g, _ := st.Get(ctx, live.ID); g.Status != gate.Open {
		t.Errorf("today's held gate of an open task is %s", g.Status)
	}
}
