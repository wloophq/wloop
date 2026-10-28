package cli

import (
	"context"
	"fmt"
	"maps"
	"time"

	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	"github.com/wloophq/wloop/internal/core/gates"
	coretasks "github.com/wloophq/wloop/internal/core/tasks"
	"github.com/wloophq/wloop/internal/domain/gate"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/workflow"
	intentload "github.com/wloophq/wloop/internal/intent"
)

// newGates is the daemon's one gates service, built on first use: the
// gates of db, worded and decided by the profile as it loads now, each on
// the task of its unit.
func (e *Env) newGates(db *sqlite.DB, tasks *coretasks.Service, logf func(string, ...any)) *gates.Service {
	if e.gates == nil {
		st := db.Gates(e.Now)
		e.gateReader = st
		e.gates = gates.New(gates.Deps{Store: st, Reader: st, Profile: profileGates{e.loadedIntents}, Tasks: gateTasks{db.Tasks(), tasks},
			Policy: profilePolicy{e.loadedIntents}, Clock: clockFunc(e.Now), Log: logf})
	}
	return e.gates
}

// profileGates are the gates of gates.toml as the profile loads now.
type profileGates struct{ loaded func() *intentload.Loaded }

func (p profileGates) Gate(name string) (gates.Def, bool) {
	g, ok := p.loaded().Gates.Gate(name)
	return gateDef(g), ok
}

func (p profileGates) FirstOf(k gate.Kind) (gates.Def, bool) {
	g, ok := p.loaded().Gates.FirstOf(k)
	return gateDef(g), ok
}

func gateDef(g intentload.GateDef) gates.Def {
	d := gates.Def{Name: g.Name, Kind: g.Kind, Label: g.Label}
	for _, in := range g.Intents {
		d.Intents = append(d.Intents, gates.Intent{Name: in.Name, Status: in.Status, Answer: in.Answer})
	}
	return d
}

// profilePolicy is gates.toml's [approval], over the gate's own facts.
type profilePolicy struct{ loaded func() *intentload.Loaded }

func (p profilePolicy) Decide(_ context.Context, g gate.Gate) (gates.Decision, error) {
	m, w, at, err := p.loaded().Gates.Approval.Decide(intentload.GateFacts(g))
	return gates.Decision{Mode: m, Window: w, Rule: at}, err
}

// workflowPolicy is the approval of the gates w's gate.open steps open on a
// unit whose facts are facts, read with the gate's own.
type workflowPolicy struct {
	w     workflow.Workflow
	facts workflow.Facts
	now   time.Time
}

func (p workflowPolicy) Decide(_ context.Context, g gate.Gate) (gates.Decision, error) {
	a, ok := p.w.Gates[g.Kind]
	if g.Kind == gate.KindPlan {
		a, ok = p.w.Approval, true
	}
	if !ok {
		return gates.Decision{Mode: gate.ModeWait}, fmt.Errorf("workflow %s opens no %s gate", p.w.Name, g.Kind)
	}
	facts := maps.Clone(p.facts)
	if facts == nil {
		facts = workflow.Facts{}
	}
	maps.Copy(facts, intentload.GateFacts(g))
	for i, r := range a.Rules {
		ok, err := r.When.Match(workflow.Vars{Facts: facts, Now: p.now})
		if err != nil {
			return gates.Decision{Mode: gate.ModeWait}, fmt.Errorf("workflows/%s gates.approval rule %d: %w", p.w.Name, i, err)
		}
		if ok {
			return gates.Decision{Mode: gateMode(r.Mode), Window: r.Window, Rule: fmt.Sprintf("workflows/%s gates.approval rule %d", p.w.Name, i)}, nil
		}
	}
	return gates.Decision{Mode: gateMode(a.Mode), Window: a.Window, Rule: "workflows/" + p.w.Name + " gates.approval"}, nil
}

// gateMode is a run approval's mode as a gate's.
func gateMode(m workflow.ApprovalMode) gate.Mode {
	switch m {
	case workflow.ApprovalRun:
		return gate.ModeRun
	case workflow.ApprovalDelay:
		return gate.ModeDelay
	}
	return gate.ModeWait
}

// gateTasks finds the task a gate's unit is on in the store and opens one
// through the tasks service.
type gateTasks struct {
	store   coretasks.Reader
	service *coretasks.Service
}

func (g gateTasks) TasksOf(ctx context.Context, u unit.Unit) ([]task.Task, error) {
	return g.store.TasksOf(ctx, u)
}

func (g gateTasks) Open(ctx context.Context, r coretasks.OpenRequest) (task.Task, error) {
	return g.service.Open(ctx, r)
}
