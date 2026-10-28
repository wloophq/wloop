package automation_test

import (
	"context"
	"maps"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/core/automation"
	"github.com/wloophq/wloop/internal/core/automation/automationtest"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/workflow"
)

// scopedUnits gives each workflow its own session.settled: the time of its
// name in settled, zero when absent.
type scopedUnits struct {
	*automationtest.Units
	settled map[string]time.Time
}

var _ automation.Scoper = scopedUnits{}

func (s scopedUnits) Scope(_ context.Context, wf string, _ unit.Unit, f workflow.Facts) (workflow.Facts, error) {
	out := maps.Clone(f)
	out["session.settled"] = s.settled[wf]
	return out, nil
}

func settledWF(name string) workflow.Workflow {
	w := windowed(sweepWF(name, automationtest.MatchFunc(func(v workflow.Vars) bool {
		at, _ := v.Facts["session.settled"].(time.Time)
		return at.IsZero() || at.Before(t0)
	}, "session.settled"), "session.close"), 5*time.Minute)
	w.Key = []string{"session.settled"}
	return w
}

func TestAWorkflowReadsTheFactsScopedToIt(t *testing.T) {
	r := newRig(t, settledWF("quiet"), settledWF("busy"))
	su := scopedUnits{Units: r.units, settled: map[string]time.Time{"busy": t0.Add(time.Hour)}}
	r.e.Units, r.e.Keep = su, su
	r.put(s1, "a")
	p := r.evaluate()
	if _, ok := planItem(p, "quiet", s1); !ok {
		t.Fatalf("quiet's own settled_at is zero, so it plans: %+v", p.Items)
	}
	if it, ok := planItem(p, "busy", s1); ok {
		t.Fatalf("busy read another workflow's facts: %+v", it)
	}
	if f, _ := r.units.Facts(context.Background(), s1); f["session.settled"] != nil {
		t.Fatalf("the scope wrote into the unit's facts: %v", f)
	}
}

func TestADueRunKeysAScopedItemAsThePlanDid(t *testing.T) {
	r := newRig(t, settledWF("quiet"))
	r.e.Keep = scopedUnits{Units: r.units, settled: map[string]time.Time{"quiet": t0.Add(-time.Hour)}}
	r.e.Units = r.e.Keep.(scopedUnits)
	r.put(s1, "a")
	r.sweep()
	r.clock.Advance(6 * time.Minute)
	if err := r.e.DueRun(context.Background()); err != nil {
		t.Fatal(err)
	}
	if ran := r.ran("session.close"); len(ran) != 1 || ran[0] != s1 {
		t.Fatalf("the due item ran on %v, want once on %s", ran, s1)
	}
}
