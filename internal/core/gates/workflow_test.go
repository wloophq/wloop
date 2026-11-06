package gates_test

import (
	"context"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/core/gates"
	"github.com/wloophq/wloop/internal/domain/gate"
	"github.com/wloophq/wloop/internal/domain/workflow"
)

// factIs holds when fact name is want.
type factIs struct{ name, want string }

func (p factIs) Match(v workflow.Vars) (bool, error) { return v.Facts[p.name] == p.want, nil }
func (p factIs) Reads() []string                     { return []string{p.name} }

func TestAWorkflowsPolicyDecidesByItsFirstRuleOverTheUnitsAndTheGatesFacts(t *testing.T) {
	ctx := context.Background()
	w := workflow.Workflow{Name: "tidy",
		Approval: workflow.Approval{Mode: workflow.ApprovalDelay, Window: time.Minute},
		Gates: map[gate.Kind]workflow.Approval{gate.KindProposal: {Mode: workflow.ApprovalWait, Rules: []workflow.ApprovalRule{
			{When: factIs{"task.area", "ops"}, Mode: workflow.ApprovalWait},
			{When: factIs{"gate.name", "merge"}, Mode: workflow.ApprovalRun},
		}}}}
	gateFacts := func(g gate.Gate) workflow.Facts { return workflow.Facts{"gate.name": g.Name} }
	p := gates.WorkflowPolicy(w, workflow.Facts{"task.area": "web"}, gateFacts, time.Time{})
	for _, c := range []struct {
		g    gate.Gate
		want gates.Decision
	}{
		{gate.Gate{Kind: gate.KindProposal, Name: "merge"}, gates.Decision{Mode: gate.ModeRun, Rule: "workflows/tidy gates.approval rule 1"}},
		{gate.Gate{Kind: gate.KindProposal, Name: "relate"}, gates.Decision{Mode: gate.ModeWait, Rule: "workflows/tidy gates.approval"}},
		{gate.Gate{Kind: gate.KindPlan}, gates.Decision{Mode: gate.ModeDelay, Window: time.Minute, Rule: "workflows/tidy gates.approval"}},
	} {
		if got, err := p.Decide(ctx, c.g); err != nil || got != c.want {
			t.Errorf("%s %s: %+v, %v, want %+v", c.g.Kind, c.g.Name, got, err, c.want)
		}
	}
	if got, err := p.Decide(ctx, gate.Gate{Kind: gate.KindStep}); err == nil || got.Mode != gate.ModeWait {
		t.Errorf("a kind the workflow approves none of: %+v, %v", got, err)
	}
}
