package cli

import (
	"context"
	"errors"
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
// the task of its unit, which the daemon's tasks service opens when none is.
func (e *Env) newGates(db *sqlite.DB, logf func(string, ...any)) *gates.Service {
	if e.gates == nil {
		st := db.Gates(e.Now)
		e.gateReader = st
		e.gates = gates.New(gates.Deps{Store: st, Reader: st, Profile: profileGates{e.loadedIntents}, Tasks: gateTasks{db.Tasks(), e},
			Policy: profilePolicy{e.loadedIntents}, Clock: clockFunc(e.Now), Log: logf, Events: sqlite.New(db),
			Publisher: outboxPublisher{e}})
	}
	return e.gates
}

// outboxPublisher is the daemon's outbox as the consequence of a draft gate, read at each resolution: it is
// built after the gates.
type outboxPublisher struct{ e *Env }

var errNoOutboxYet = errors.New("the daemon's outbox is not built: the draft stays held")

func (p outboxPublisher) Publish(ctx context.Context, g gate.Gate) error {
	if p.e.outbox == nil {
		return errNoOutboxYet
	}
	return p.e.outbox.Publish(ctx, g)
}

// draftGates are the gates the drafts of db wait on, through the daemon's gates service.
type draftGates struct {
	e    *Env
	db   *sqlite.DB
	logf func(string, ...any)
}

func (g draftGates) Open(ctx context.Context, gt gate.Gate, p gates.Policy) (gate.Gate, error) {
	return g.e.newGates(g.db, g.logf).Open(ctx, gt, p)
}

func (g draftGates) Supersede(ctx context.Context, id int64, by task.Actor) error {
	return g.e.newGates(g.db, g.logf).Supersede(ctx, id, by)
}

func (g draftGates) RecordSent(ctx context.Context, gt gate.Gate) (gate.Gate, error) {
	return g.e.newGates(g.db, g.logf).RecordSent(ctx, gt)
}

func (g draftGates) Where(ctx context.Context, q gates.Query) ([]gate.Gate, error) {
	g.e.newGates(g.db, g.logf)
	return g.e.gateReader.Where(ctx, q)
}

// serviceTaskGates are the gates a gated verb opens, through the gates
// service, and reads back.
type serviceTaskGates struct {
	e  *Env
	db *sqlite.DB
}

func (g serviceTaskGates) Open(ctx context.Context, gt gate.Gate) (gate.Gate, error) {
	return g.e.newGates(g.db, nil).Open(ctx, gt, nil)
}

func (g serviceTaskGates) Get(ctx context.Context, id int64) (gate.Gate, error) {
	g.e.newGates(g.db, nil)
	return g.e.gateReader.Get(ctx, id)
}

func (g serviceTaskGates) OfTask(ctx context.Context, id task.ID) ([]gate.Gate, error) {
	g.e.newGates(g.db, nil)
	return g.e.gateReader.OfTask(ctx, id)
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
	m, w, at, err := p.loaded().Gates.Approval.Decide(intentload.GateFacts(g, ""))
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
	maps.Copy(facts, intentload.GateFacts(g, ""))
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
// through the daemon's tasks service.
type gateTasks struct {
	store coretasks.Reader
	e     *Env
}

func (g gateTasks) TasksOf(ctx context.Context, u unit.Unit) ([]task.Task, error) {
	return g.store.TasksOf(ctx, u)
}

var errNoTasksService = errors.New("no tasks service opens the gate's task: the daemon serves it")

func (g gateTasks) Open(ctx context.Context, r coretasks.OpenRequest) (task.Task, error) {
	if g.e.tasks == nil {
		return task.Task{}, errNoTasksService
	}
	return g.e.tasks.Open(ctx, r)
}
