package automation_test

import (
	"context"
	"slices"
	"sync"
	"testing"

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
