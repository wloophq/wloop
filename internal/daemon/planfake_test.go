package daemon

import (
	"context"
	"slices"
	"sync"
	"time"

	"github.com/wloophq/wloop/internal/core/automation"
	"github.com/wloophq/wloop/internal/core/gates"
	"github.com/wloophq/wloop/internal/core/gates/gatestest"
	"github.com/wloophq/wloop/internal/domain/gate"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/workflow"
	"github.com/wloophq/wloop/internal/intent/builtin"
	"github.com/wloophq/wloop/internal/readmodel/overlay"
)

// pendingPlan is a plan whose only items are the pending rounds set on it,
// one follow-up per unit; it serves those rounds to the rows too. With
// gates, each pending round waits on its plan gate, which the plan opens and
// closes as the engine does.
type pendingPlan struct {
	mu      sync.Mutex
	now     time.Time
	pending map[unit.Unit]bool
	acted   []workflow.Action
	guided  []string
	gates   *gates.Service
	reader  gates.Reader
}

// gated serves s the plan gates of p over an in-memory store, which s
// releases.
func (p *pendingPlan) gated(s *Server) {
	st := gatestest.NewStore(func() time.Time { return p.now })
	p.gates = gates.New(gates.Deps{Store: st, Reader: st, Profile: profileGates{s.loaded().Gates}, Clock: fixedClock(p.now), Releaser: s, Unblocker: s})
	p.reader = st
	s.deps.Gates, s.deps.GateReader = p.gates, st
}

func itemID(u unit.Unit) string { return "item-" + u.Key }

// gateOf is the open plan gate of u's round; zero for none.
func (p *pendingPlan) gateOf(u unit.Unit) gate.Gate {
	if p.reader == nil {
		return gate.Gate{}
	}
	open, _ := p.reader.Where(context.Background(), gates.Query{Kind: gate.KindPlan, Unit: u})
	for _, g := range open {
		if g.Subject == itemID(u) {
			return g
		}
	}
	return gate.Gate{}
}

// close supersedes the open gate of u's round, which left the plan.
func (p *pendingPlan) close(u unit.Unit) {
	if g := p.gateOf(u); g.ID != 0 {
		_ = p.gates.Supersede(context.Background(), g.ID, task.ActorWorkflow)
	}
}

func newPendingPlan(now time.Time) *pendingPlan {
	return &pendingPlan{now: now, pending: map[unit.Unit]bool{}}
}

func (p *pendingPlan) propose(u unit.Unit) {
	p.mu.Lock()
	p.pending[u] = true
	p.mu.Unlock()
	if p.gates != nil && p.gateOf(u).ID == 0 {
		if _, err := p.gates.Open(context.Background(), gate.Gate{Kind: gate.KindPlan, Unit: u, Subject: itemID(u)}, holdGate{}); err != nil {
			panic(err)
		}
	}
}

// holdGate holds a gate for a person.
type holdGate struct{}

func (holdGate) Decide(context.Context, gate.Gate) (gates.Decision, error) {
	return gates.Decision{Mode: gate.ModeWait}, nil
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
		if f.Unit != nil && *f.Unit != u || f.ID != "" && f.ID != itemID(u) {
			continue
		}
		out = append(out, workflow.Item{ID: itemID(u), Workflow: "review-follow-up", Unit: u, Decision: workflow.DecisionPending,
			Reason: "ana replied", Steps: []workflow.Step{{Use: builtin.StepTurnStart}}, Default: workflow.OutcomeRun,
			Deadline: p.now.Add(time.Minute), At: p.now})
	}
	return out, nil
}

func (p *pendingPlan) Act(_ context.Context, id string, a workflow.Action, guidance string) (workflow.Item, error) {
	p.mu.Lock()
	var acted *unit.Unit
	for u := range p.pending {
		if itemID(u) == id {
			delete(p.pending, u)
			p.acted = append(p.acted, a)
			if guidance != "" {
				p.guided = append(p.guided, guidance)
			}
			acted = &u
			break
		}
	}
	p.mu.Unlock()
	if acted == nil {
		return workflow.Item{}, automation.Refused{Reason: "no item " + id}
	}
	p.close(*acted)
	return workflow.Item{ID: id, Unit: *acted}, nil
}

func (p *pendingPlan) Drop(_ context.Context, u unit.Unit, _ string) error {
	p.mu.Lock()
	delete(p.pending, u)
	p.mu.Unlock()
	p.close(u)
	return nil
}

// Release carries a resolved plan gate onto its round as the engine does.
func (p *pendingPlan) Release(ctx context.Context, g gate.Gate) error {
	a := workflow.ActionApply
	switch g.Status {
	case gate.Rejected:
		a = workflow.ActionSkip
	case gate.Dismissed:
		a = workflow.ActionDismiss
	default:
		if g.Answer != "" {
			a = workflow.ActionGuide
		}
	}
	_, err := p.Act(ctx, g.Subject, a, g.Answer)
	return err
}

func (p *pendingPlan) Plans(context.Context) (map[unit.Unit]overlay.Pending, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := map[unit.Unit]overlay.Pending{}
	for u := range p.pending {
		out[u] = overlay.Pending{Intent: "follow-up", Reason: "ana replied", Since: p.now, Deadline: p.now.Add(time.Minute)}
	}
	return out, nil
}

// withRowGates serves s the gates of an in-memory store, whose plan and
// trust gates s applies.
func withRowGates(s *Server) (*gates.Service, *gatestest.Store) {
	st := gatestest.NewStore(s.deps.Now)
	svc := gates.New(gates.Deps{Store: st, Reader: st, Profile: profileGates{s.loaded().Gates}, Clock: clockFunc(s.deps.Now), Releaser: s, Unblocker: s})
	s.deps.Gates, s.deps.GateReader = svc, st
	return svc, st
}
