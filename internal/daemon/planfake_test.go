package daemon

import (
	"context"
	"slices"
	"sync"
	"time"

	"github.com/wloophq/wloop/internal/core/automation"
	"github.com/wloophq/wloop/internal/domain/round"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/workflow"
	"github.com/wloophq/wloop/internal/intent/builtin"
)

// pendingPlan is a plan whose only items are the pending rounds set on it,
// one follow-up per unit; it serves those rounds to the rows too.
type pendingPlan struct {
	mu      sync.Mutex
	now     time.Time
	pending map[unit.Unit]bool
	acted   []workflow.Action
}

func newPendingPlan(now time.Time) *pendingPlan {
	return &pendingPlan{now: now, pending: map[unit.Unit]bool{}}
}

func (p *pendingPlan) propose(u unit.Unit) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.pending[u] = true
}

func (p *pendingPlan) isPending(u unit.Unit) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.pending[u]
}

func (p *pendingPlan) Evaluate(context.Context, workflow.TriggerKind) (automation.Plan, error) {
	return automation.Plan{}, nil
}

func (p *pendingPlan) Items(_ context.Context, f automation.Filter) ([]workflow.Item, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(f.Decisions) > 0 && !slices.Contains(f.Decisions, workflow.DecisionPending) {
		return nil, nil
	}
	var out []workflow.Item
	for u := range p.pending {
		if f.Unit != nil && *f.Unit != u {
			continue
		}
		out = append(out, workflow.Item{ID: "item-" + u.Key, Workflow: "review-follow-up", Unit: u, Decision: workflow.DecisionPending,
			Reason: "ana replied", Steps: []workflow.Step{{Use: builtin.StepTurnStart}}, Default: workflow.OutcomeRun,
			Deadline: p.now.Add(time.Minute), At: p.now})
	}
	return out, nil
}

func (p *pendingPlan) Act(_ context.Context, id string, a workflow.Action, _ string) (workflow.Item, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for u := range p.pending {
		if "item-"+u.Key == id {
			delete(p.pending, u)
			p.acted = append(p.acted, a)
			return workflow.Item{ID: id, Unit: u}, nil
		}
	}
	return workflow.Item{}, automation.Refused{Reason: "no item " + id}
}

func (p *pendingPlan) Drop(_ context.Context, u unit.Unit, _ string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.pending, u)
	return nil
}

func (p *pendingPlan) DueRun(context.Context) error { return nil }

func (p *pendingPlan) Pending(context.Context) (map[unit.Unit]round.Pending, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := map[unit.Unit]round.Pending{}
	for u := range p.pending {
		out[u] = round.Pending{Intent: "follow-up", Reason: "ana replied", Since: p.now, Deadline: p.now.Add(time.Minute)}
	}
	return out, nil
}
