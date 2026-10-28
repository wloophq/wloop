package automation

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/wloophq/wloop/internal/core/gates"
	"github.com/wloophq/wloop/internal/domain/gate"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/workflow"
)

// gated says whether it waits on a plan gate: a pending item of a workflow
// that applies, which a person decides or its window's end runs.
func (e *Engine) gated(it workflow.Item) bool {
	return e.Gates != nil && it.Decision == workflow.DecisionPending && !it.Observe
}

// itemSubject says whether a plan gate's subject names a plan item, whose
// id joins its workflow, unit and key with slashes; a gate.open step's names
// its workflow alone.
func itemSubject(s string) bool { return strings.Contains(s, "/") }

// decided is a decision the planner took, as the policy its gate opens
// under.
type decided gates.Decision

func (d decided) Decide(context.Context, gate.Gate) (gates.Decision, error) {
	return gates.Decision(d), nil
}

// decisionOf is how it's gate waits: until its Deadline, when its window
// runs it, else for a person.
func decisionOf(it workflow.Item, now time.Time) gates.Decision {
	if it.Default != workflow.OutcomeRun {
		return gates.Decision{Mode: gate.ModeWait}
	}
	return gates.Decision{Mode: gate.ModeDelay, Window: max(it.Deadline.Sub(now), time.Nanosecond)}
}

// syncGates opens the plan gate of every gated item that has none, and
// supersedes each open plan gate whose item no longer waits.
func (e *Engine) syncGates(ctx context.Context) error {
	if e.Gates == nil {
		return nil
	}
	e.gateMu.Lock()
	defer e.gateMu.Unlock()
	items, err := e.Ledger.Items(ctx, Filter{Decisions: []workflow.Decision{workflow.DecisionPending}})
	if err != nil {
		return err
	}
	open, err := e.Gates.Where(ctx, gates.Query{Kind: gate.KindPlan})
	if err != nil {
		return err
	}
	has := map[string]bool{}
	for _, g := range open {
		has[g.Subject] = true
	}
	now := e.now()
	waiting := map[string]bool{}
	var errs []error
	for _, it := range items {
		if !e.gated(it) {
			continue
		}
		waiting[it.ID] = true
		if has[it.ID] {
			continue
		}
		if _, err := e.Gates.Open(ctx, gate.Gate{Kind: gate.KindPlan, Unit: it.Unit, Subject: it.ID}, decided(decisionOf(it, now))); err != nil {
			errs = append(errs, fmt.Errorf("opening the gate of %s: %w", it.ID, err))
		}
	}
	for _, g := range open {
		if !itemSubject(g.Subject) || waiting[g.Subject] {
			continue
		}
		if err := e.Gates.Supersede(ctx, g.ID, task.ActorWorkflow); err != nil && !errors.Is(err, gates.ErrResolved) {
			errs = append(errs, fmt.Errorf("closing the gate of %s: %w", g.Subject, err))
		}
	}
	return errors.Join(errs...)
}

// syncGatesOrLog is syncGates after a ledger write, which its failure does
// not undo: the next write tries again.
func (e *Engine) syncGatesOrLog(ctx context.Context) {
	if err := e.syncGates(ctx); err != nil {
		e.logf("plan gates: %v", err)
	}
}

// Release is a resolved plan gate's consequence on the item it names; the
// caller holds the loop lock. A person's approval runs the item by hand,
// with the gate's answer as its guidance when it has one; an approval by
// its window or a trusted rule runs it as an automatic run; a rejection
// skips it and a dismissal dismisses it. An item that no longer waits is
// left as it is.
func (e *Engine) Release(ctx context.Context, g gate.Gate) error {
	items, err := e.Ledger.Items(ctx, Filter{ID: g.Subject})
	if err != nil {
		return err
	}
	if len(items) == 0 || items[0].Decision != workflow.DecisionPending {
		e.logf("plan gate %d resolved %s: %s no longer waits", g.ID, g.Status, g.Subject)
		return nil
	}
	it := items[0]
	switch g.Status {
	case gate.Approved, gate.Answered:
		switch {
		case g.By != task.ActorPerson:
			return e.runReleased(ctx, it)
		case g.Answer != "":
			_, err = e.Act(ctx, it.ID, workflow.ActionGuide, g.Answer)
		default:
			_, err = e.Act(ctx, it.ID, workflow.ActionApply, "")
		}
	case gate.Rejected:
		_, err = e.Act(ctx, it.ID, workflow.ActionSkip, "")
	case gate.Dismissed:
		_, err = e.Act(ctx, it.ID, workflow.ActionDismiss, "")
	}
	return err
}

// runReleased runs it as a window's end does, in the round state.
func (e *Engine) runReleased(ctx context.Context, it workflow.Item) (err error) {
	pause, err := e.pause(ctx)
	if err != nil {
		return err
	}
	st, err := e.loadState(ctx)
	if err != nil {
		return err
	}
	defer func() {
		if serr := e.saveState(ctx, st); serr != nil {
			err = errors.Join(err, serr)
		}
	}()
	w, ok := e.live()[it.Workflow]
	if !ok || w.Observe {
		e.logf("%s: not run, its workflow is not loaded or observes", it.ID)
		return nil
	}
	_, err = e.dueOne(ctx, w, it, pause)
	return err
}

// Waiting is a pending item and the open plan gate it waits on.
type Waiting struct {
	Item workflow.Item
	Gate gate.Gate
}

// Waiting are the pending items that wait on an open plan gate, with their
// gates, oldest gate first: u's alone when u is set. None with no Gates.
func (e *Engine) Waiting(ctx context.Context, u *unit.Unit) ([]Waiting, error) {
	if e.Gates == nil {
		return nil, nil
	}
	q := gates.Query{Kind: gate.KindPlan}
	if u != nil {
		q.Unit = *u
	}
	open, err := e.Gates.Where(ctx, q)
	if err != nil {
		return nil, err
	}
	items, err := e.Ledger.Items(ctx, Filter{Unit: u, Decisions: []workflow.Decision{workflow.DecisionPending}})
	if err != nil {
		return nil, err
	}
	byID := make(map[string]workflow.Item, len(items))
	for _, it := range items {
		byID[it.ID] = it
	}
	var out []Waiting
	for _, g := range open {
		if it, ok := byID[g.Subject]; ok && e.gated(it) {
			out = append(out, Waiting{Item: it, Gate: g})
		}
	}
	return out, nil
}
