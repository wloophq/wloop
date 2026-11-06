package automation_test

import (
	"context"
	"maps"
	"slices"
	"sync"
	"testing"

	"github.com/wloophq/wloop/internal/core/automation"
	"github.com/wloophq/wloop/internal/core/automation/automationtest"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/workflow"
)

// readsUnits records the reads each Facts call's ctx carries.
type readsUnits struct {
	*automationtest.Units
	mu    sync.Mutex
	reads [][]string
}

func (r *readsUnits) Facts(ctx context.Context, u unit.Unit) (workflow.Facts, error) {
	r.mu.Lock()
	r.reads = append(r.reads, workflow.ReadsOf(ctx))
	r.mu.Unlock()
	return r.Units.Facts(ctx, u)
}

func TestAWorkflowsFactsAreReadWithWhatItsWhenAndStepsName(t *testing.T) {
	w := sweepWF("reads", automationtest.MatchFunc(func(workflow.Vars) bool { return true }, "session.costly"), "session.close")
	w.Steps[0].Args = map[string]any{"with": "session.named_by_a_step"}
	r := newRig(t, w)
	ru := &readsUnits{Units: r.units}
	r.e.Units = ru
	r.put(s1, "a")
	r.evaluate()
	if len(ru.reads) == 0 {
		t.Fatal("no facts read")
	}
	for _, got := range ru.reads {
		if !slices.Contains(got, "session.costly") || !slices.Contains(got, "session.named_by_a_step") {
			t.Fatalf("a facts read carried %v, want the when's and the step's reads", got)
		}
	}
}

// costlyUnits serve session.costly computed only for a reader that names it, as a costly fact's provider does.
type costlyUnits struct{ *automationtest.Units }

func (c costlyUnits) Facts(ctx context.Context, u unit.Unit) (workflow.Facts, error) {
	f, err := c.Units.Facts(ctx, u)
	if err != nil {
		return f, err
	}
	out := maps.Clone(f)
	out["session.costly"] = workflow.Unrequested{Fact: "session.costly"}
	if slices.Contains(workflow.ReadsOf(ctx), "session.costly") {
		out["session.costly"] = []string{"s-2"}
	}
	return out, nil
}

// factStep is steps whose Run reads its unit's facts, as a step reading the fact an arg names does.
type factStep struct {
	automation.Steps
	units automation.Units
	saw   []any
}

func (s *factStep) Run(ctx context.Context, st workflow.Step, u unit.Unit, why workflow.Why, o automation.RunOpts) (string, error) {
	f, err := s.units.Facts(ctx, u)
	if err != nil {
		return "", err
	}
	s.saw = append(s.saw, f["session.costly"])
	return s.Steps.Run(ctx, st, u, why, o)
}

func TestAStepThatNamesACostlyFactReadsItComputed(t *testing.T) {
	w := sweepWF("with", automationtest.MatchFunc(func(workflow.Vars) bool { return true }), "session.close")
	w.Steps[0].Args = map[string]any{"with": "session.costly"}
	r := newRig(t, w)
	units := costlyUnits{r.units}
	steps := &factStep{Steps: r.e.Steps, units: units}
	r.e.Units, r.e.Steps = units, steps
	r.put(s1, "a")
	r.sweep()
	if len(steps.saw) == 0 {
		t.Fatal("the step never ran")
	}
	if got, ok := steps.saw[0].([]string); len(steps.saw) != 1 || !ok || !slices.Equal(got, []string{"s-2"}) {
		t.Fatalf("the step read session.costly as %#v, want it computed", steps.saw)
	}
}
