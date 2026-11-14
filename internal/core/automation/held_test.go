package automation_test

import (
	"context"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/core/automation"
	"github.com/wloophq/wloop/internal/core/lock"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/workflow"
)

type heldSteps struct {
	automation.Steps
	held []bool
}

func (s *heldSteps) Run(ctx context.Context, st workflow.Step, u unit.Unit, why workflow.Why, o automation.RunOpts) (string, error) {
	s.held = append(s.held, lock.Held(ctx))
	return s.Steps.Run(ctx, st, u, why, o)
}

func TestASweepRunsItsStepsUnderTheHeldLoop(t *testing.T) {
	r := newRig(t, sweepWF("close", idle, "session.close"))
	steps := &heldSteps{Steps: r.steps}
	r.e.Steps = steps
	r.put(s1, "a")
	r.sweep()
	if len(steps.held) != 1 || !steps.held[0] {
		t.Fatalf("held per run = %v, want one run that sees the loop held", steps.held)
	}
}

func TestADueRunRunsItsStepsUnderTheHeldLoop(t *testing.T) {
	r := newRig(t, windowed(sweepWF("close", idle, "session.close"), 5*time.Minute))
	steps := &heldSteps{Steps: r.steps}
	r.e.Steps = steps
	r.put(s1, "a")
	r.sweep()
	r.clock.Advance(time.Hour)
	if err := r.e.DueRun(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(steps.held) != 1 || !steps.held[0] {
		t.Fatalf("held per run = %v, want one due run that sees the loop held", steps.held)
	}
}
