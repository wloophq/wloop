package automation_test

import (
	"context"
	"testing"

	"github.com/wloophq/wloop/internal/core/automation/automationtest"
	"github.com/wloophq/wloop/internal/domain/unit"
)

// skippingKeep is the rig's Keep, skipping workflow on the units of skipped.
type skippingKeep struct {
	*automationtest.Units
	skipped map[string][]unit.Unit
	asked   int
}

func (k *skippingKeep) Skipped(_ context.Context, workflow string, u unit.Unit) (bool, error) {
	k.asked++
	for _, s := range k.skipped[workflow] {
		if s == u {
			return true, nil
		}
	}
	return false, nil
}

func TestTheEngineSkipsASkippedSubject(t *testing.T) {
	r := newRig(t, sweepWF("close", idle, "session.close"))
	r.put(s1, "a")
	r.put(s2, "b")
	keep := &skippingKeep{Units: r.units, skipped: map[string][]unit.Unit{"close": {s1}}}
	r.e.Keep = keep
	p := r.evaluate()
	if _, ok := planItem(p, "close", s1); ok {
		t.Error("a skipped subject was planned")
	}
	if _, ok := planItem(p, "close", s2); !ok {
		t.Error("a subject not skipped was not planned")
	}
	if keep.asked == 0 {
		t.Error("the engine never asked its Keep")
	}
}
