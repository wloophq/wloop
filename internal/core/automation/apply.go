package automation

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/workflow"
)

// outcome is how running an item ended.
type outcome int

const (
	outcomeDone outcome = iota
	outcomeRefused
	outcomeDeferred
	outcomeTransient
	outcomeFailed
)

// result is how running an item ended; err is the step's error, nil when done.
type result struct {
	it      workflow.Item
	outcome outcome
	err     error
}

// runItem runs an item's steps in order, stopping at the first that does not
// run, and records the outcome: done, refused (blocked, attempts untouched),
// deferred (pending until a later run is done), failed (one more attempt).
// A transient failure records nothing. The error is the ledger's.
func (e *Engine) runItem(ctx context.Context, it workflow.Item, o RunOpts) (result, error) {
	now := e.now()
	// Resolved before the first step: a step can end the ownership, as
	// workspace.remove drops the workspace's units.
	owners, err := e.owners(ctx, it.Unit)
	if err != nil {
		return e.stopped(ctx, it, Transient{Err: fmt.Errorf("reading the owners of %s: %w", it.Unit.Label(), err)}, nil, now)
	}
	var evs []port.Event
	for i, st := range it.Steps {
		detail, err := e.Steps.Run(ctx, st, it.Unit, it.Why, o)
		if err == nil {
			evs = append(evs, ran(it, owners, fmt.Sprintf("%s:%d", it.ID, i), detail, now)...)
			continue
		}
		return e.stopped(ctx, it, err, evs, now)
	}
	it.Decision, it.Reason, it.Error = workflow.DecisionRun, "", ""
	r := workflow.Run{Key: it.Key, Status: workflow.RunDone, Attempts: it.Attempts, At: now}
	return result{it: it, outcome: outcomeDone}, e.record(ctx, r, it, evs)
}

func (e *Engine) stopped(ctx context.Context, it workflow.Item, err error, evs []port.Event, now time.Time) (result, error) {
	var refused Refused
	var deferred Deferred
	var transient Transient
	switch {
	case errors.As(err, &refused):
		it.Decision, it.Reason = workflow.DecisionBlocked, refused.Reason
		evs = append(evs, event(it, workflow.KindStepRefused, fmt.Sprintf("%s:%d", it.ID, it.Attempts), refused.Reason, now))
		r := workflow.Run{Key: it.Key, Status: workflow.RunRefused, Attempts: it.Attempts, Error: refused.Reason, At: now}
		return result{it, outcomeRefused, err}, e.record(ctx, r, it, evs)
	case errors.As(err, &deferred):
		it.Decision, it.Reason = workflow.DecisionPending, deferred.Reason
		it.Default, it.Deadline = workflow.OutcomeRun, now.Add(e.retryBase())
		return result{it, outcomeDeferred, nil}, e.replace(ctx, it, evs)
	case errors.As(err, &transient) || ctx.Err() != nil:
		e.logf("%s: %v; retried on the next sweep", it.ID, err)
		return result{it, outcomeTransient, err}, nil
	}
	it.Attempts++
	it.Error = err.Error()
	if it.Attempts >= workflow.MaxAttempts {
		it.Decision, it.Reason = workflow.DecisionBlocked, fmt.Sprintf("failed %d times: %s", it.Attempts, it.Error)
	} else {
		it.Decision, it.Reason = workflow.DecisionSkipped, "retrying after "+it.Error
	}
	evs = append(evs, event(it, workflow.KindStepFailed, fmt.Sprintf("%s:%d", it.ID, it.Attempts), it.Error, now))
	r := workflow.Run{Key: it.Key, Status: workflow.RunFailed, Attempts: it.Attempts, Error: it.Error, At: now}
	return result{it, outcomeFailed, err}, e.record(ctx, r, it, evs)
}

func (e *Engine) record(ctx context.Context, r workflow.Run, it workflow.Item, evs []port.Event) error {
	if err := e.Ledger.Record(ctx, r, it, evs...); err != nil {
		return fmt.Errorf("recording %s: %w", it.ID, err)
	}
	e.publish(evs)
	return nil
}

// replace puts it in place of the stored item with its id, keeping the other
// items of its workflow.
func (e *Engine) replace(ctx context.Context, it workflow.Item, evs []port.Event) error {
	items, err := e.Ledger.Items(ctx, Filter{Workflow: it.Workflow})
	if err != nil {
		return err
	}
	items = slices.DeleteFunc(items, func(o workflow.Item) bool { return o.ID == it.ID })
	return e.save(ctx, PlanWrite{Workflows: []string{it.Workflow}, Items: append(items, it)}, evs)
}

func (e *Engine) save(ctx context.Context, p PlanWrite, evs []port.Event) error {
	if err := e.Ledger.Save(ctx, p, evs...); err != nil {
		return fmt.Errorf("saving the plan: %w", err)
	}
	e.publish(evs)
	return nil
}

func event(it workflow.Item, kind, key, data string, at time.Time) port.Event {
	return port.Event{Unit: it.Unit, Kind: kind, Key: key, Data: data, At: at}
}

// owners are the units of work u belongs to; none for a unit gone or a
// provider that knows no owners.
func (e *Engine) owners(ctx context.Context, u unit.Unit) ([]unit.Unit, error) {
	o, ok := e.Keep.(Owner)
	if !ok {
		return nil, nil
	}
	owners, err := o.Owners(ctx, u)
	if errors.Is(err, ErrUnitGone) {
		return nil, nil
	}
	return owners, err
}

// ran are the workflow_step_ran events of a step: one on each owner, or one
// on the subject when it has none, by the workflow.
func ran(it workflow.Item, owners []unit.Unit, key, detail string, at time.Time) []port.Event {
	if len(owners) == 0 {
		owners = []unit.Unit{it.Unit}
	}
	evs := make([]port.Event, 0, len(owners))
	for _, u := range owners {
		evs = append(evs, port.Event{Unit: u, Kind: workflow.KindStepRan, Key: key, Actor: it.Workflow, Data: detail, At: at})
	}
	return evs
}

// Act carries the user's action on a plan item; the caller holds the loop
// lock. Apply runs the item by hand: the visible default is lifted, the
// guards hold, and a blocked item's attempts start over. Guide runs it at
// once with the guidance. Skip and dismiss are terminal for the item's key.
func (e *Engine) Act(ctx context.Context, id string, a workflow.Action, guidance string) (_ workflow.Item, err error) {
	items, err := e.Ledger.Items(ctx, Filter{ID: id})
	if err != nil {
		return workflow.Item{}, err
	}
	if len(items) == 0 {
		return workflow.Item{}, fmt.Errorf("no plan item %s", id)
	}
	it := items[0]
	st, err := e.loadState(ctx)
	if err != nil {
		return it, err
	}
	defer func() {
		if serr := e.saveState(ctx, st); serr != nil {
			err = errors.Join(err, serr)
		}
	}()
	switch a {
	case workflow.ActionApply:
		it.Attempts = 0
		it, err = e.byHand(ctx, it, RunOpts{ByHand: true, Confirmed: true})
	case workflow.ActionGuide:
		it.Guidance = guidance
		it, err = e.byHand(ctx, it, RunOpts{Guidance: guidance, Confirmed: true})
	case workflow.ActionSkip, workflow.ActionDismiss:
		it, err = e.dismiss(ctx, it)
	default:
		return it, fmt.Errorf("unknown action %q", a)
	}
	return it, err
}

// byHand checks every step's guards and that the item's source did not move
// before running any step, then runs the item.
func (e *Engine) byHand(ctx context.Context, it workflow.Item, o RunOpts) (workflow.Item, error) {
	moved, err := e.sourceMoved(ctx, it)
	if err != nil {
		return it, err
	}
	if w, ok := e.live()[it.Workflow]; ok && !moved {
		facts, err := e.factsFor(ctx, w, it.Unit)
		if err != nil {
			return it, err
		}
		moved = keyMoved(w, it, facts)
	}
	if moved {
		return outcomeOf(e.stopped(ctx, it, Refused{Reason: whyMoved}, nil, e.now()))
	}
	for _, st := range it.Steps {
		refusal, err := e.Steps.Check(ctx, st, it.Unit, it.Why, CheckOpts{Observe: it.Observe, ByHand: true})
		if err != nil {
			return it, err
		}
		if refusal != "" {
			return outcomeOf(e.stopped(ctx, it, Refused{Reason: refusal}, nil, e.now()))
		}
	}
	return outcomeOf(e.runItem(ctx, it, o))
}

// outcomeOf is a by-hand run's item and error: the ledger's, else the step's.
func outcomeOf(r result, err error) (workflow.Item, error) {
	if err != nil {
		return r.it, err
	}
	return r.it, r.err
}

func (e *Engine) dismiss(ctx context.Context, it workflow.Item) (workflow.Item, error) {
	for _, st := range it.Steps {
		if err := e.Steps.Dismiss(ctx, st, it.Unit, it.Why); err != nil {
			return it, err
		}
	}
	now := e.now()
	r := workflow.Run{Key: it.Key, Status: workflow.RunDismissed, Attempts: it.Attempts, At: now}
	return it, e.record(ctx, r, it, []port.Event{event(it, workflow.KindItemDismissed, it.ID, "", now)})
}

// Drop discards u's pending items without marking their keys: the next
// evaluation plans them again if they still match.
func (e *Engine) Drop(ctx context.Context, u unit.Unit, why string) error {
	pending, err := e.Ledger.Items(ctx, Filter{Unit: &u, Decisions: []workflow.Decision{workflow.DecisionPending}})
	if err != nil {
		return err
	}
	now := e.now()
	byWorkflow := map[string][]workflow.Item{}
	var order []string
	for _, it := range pending {
		if _, ok := byWorkflow[it.Workflow]; !ok {
			order = append(order, it.Workflow)
		}
		byWorkflow[it.Workflow] = append(byWorkflow[it.Workflow], it)
	}
	for _, wf := range order {
		all, err := e.Ledger.Items(ctx, Filter{Workflow: wf})
		if err != nil {
			return err
		}
		var evs []port.Event
		for _, it := range byWorkflow[wf] {
			all = slices.DeleteFunc(all, func(o workflow.Item) bool { return o.ID == it.ID })
			evs = append(evs, event(it, workflow.KindItemDropped, it.ID+":"+now.UTC().Format(time.RFC3339Nano), why, now))
		}
		if err := e.save(ctx, PlanWrite{Workflows: []string{wf}, Items: all}, evs); err != nil {
			return err
		}
	}
	return nil
}

func (e *Engine) loadState(ctx context.Context) (State, error) {
	if e.State == nil {
		return nil, nil
	}
	st, err := e.State.Load(ctx)
	if err != nil {
		return nil, fmt.Errorf("loading the round state: %w", err)
	}
	return st, nil
}

func (e *Engine) saveState(ctx context.Context, st State) error {
	if st == nil {
		return nil
	}
	if err := st.Save(ctx); err != nil {
		return fmt.Errorf("saving the round state: %w", err)
	}
	return nil
}
