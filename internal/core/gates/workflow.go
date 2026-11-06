package gates

import (
	"context"
	"fmt"
	"maps"
	"time"

	"github.com/wloophq/wloop/internal/domain/gate"
	"github.com/wloophq/wloop/internal/domain/workflow"
)

// WorkflowPolicy is the approval of the gates workflow w opens on a unit whose facts are facts: the
// [gates.approval] of the gate's kind, w's run approval for a plan gate, its first rule that holds over facts
// and gateFacts of the gate at now, else its mode and window. A gate of a kind w approves none of waits.
func WorkflowPolicy(w workflow.Workflow, facts workflow.Facts, gateFacts func(gate.Gate) workflow.Facts, now time.Time) Policy {
	return workflowPolicy{w: w, facts: facts, gateFacts: gateFacts, now: now}
}

type workflowPolicy struct {
	w         workflow.Workflow
	facts     workflow.Facts
	gateFacts func(gate.Gate) workflow.Facts
	now       time.Time
}

func (p workflowPolicy) Decide(_ context.Context, g gate.Gate) (Decision, error) {
	a, ok := p.w.Gates[g.Kind]
	if g.Kind == gate.KindPlan {
		a, ok = p.w.Approval, true
	}
	if !ok {
		return Decision{Mode: gate.ModeWait}, fmt.Errorf("workflow %s opens no %s gate", p.w.Name, g.Kind)
	}
	facts := maps.Clone(p.facts)
	if facts == nil {
		facts = workflow.Facts{}
	}
	if p.gateFacts != nil {
		maps.Copy(facts, p.gateFacts(g))
	}
	for i, r := range a.Rules {
		ok, err := r.When.Match(workflow.Vars{Facts: facts, Now: p.now})
		if err != nil {
			return Decision{Mode: gate.ModeWait}, fmt.Errorf("workflows/%s gates.approval rule %d: %w", p.w.Name, i, err)
		}
		if ok {
			return Decision{Mode: approvalMode(r.Mode), Window: r.Window, Rule: fmt.Sprintf("workflows/%s gates.approval rule %d", p.w.Name, i)}, nil
		}
	}
	return Decision{Mode: approvalMode(a.Mode), Window: a.Window, Rule: "workflows/" + p.w.Name + " gates.approval"}, nil
}

// approvalMode is a run approval's mode as a gate's.
func approvalMode(m workflow.ApprovalMode) gate.Mode {
	switch m {
	case workflow.ApprovalRun:
		return gate.ModeRun
	case workflow.ApprovalDelay:
		return gate.ModeDelay
	}
	return gate.ModeWait
}
