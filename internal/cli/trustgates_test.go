package cli

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/core/gates"
	"github.com/wloophq/wloop/internal/core/gates/gatestest"
	"github.com/wloophq/wloop/internal/daemon"
	"github.com/wloophq/wloop/internal/domain/gate"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
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
