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

// DueRun runs the deferred items whose Deadline passed, and, with no Gates,
// the pending items whose window ended with default run; the caller holds
// the loop lock.
func (e *Engine) DueRun(ctx context.Context) (err error) {
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
	_, err = e.dueRun(ctx, pause)
	return err
}

// dueDecisions are the decisions of the items a passed Deadline runs: a
// gated item's window is its gate's.
func (e *Engine) dueDecisions() []workflow.Decision {
	if e.Gates != nil {
		return []workflow.Decision{workflow.DecisionDeferred}
	}
	return []workflow.Decision{workflow.DecisionPending, workflow.DecisionDeferred}
}

// dueRun runs the due items of workflows still loaded and not observing whose
// unit the pause does not hold.
func (e *Engine) dueRun(ctx context.Context, pause Pause) (tally, error) {
	var t tally
	items, err := e.Ledger.Items(ctx, Filter{Decisions: e.dueDecisions()})
	if err != nil {
		return t, err
	}
	now := e.now()
	live := e.live()
	for _, it := range items {
		w, ok := live[it.Workflow]
		if !ok || w.Observe || it.Default != workflow.OutcomeRun || it.Deadline.After(now) {
			continue
		}
		r, err := e.dueOne(ctx, w, it, pause)
		if err != nil {
			return t, err
		}
		t.add(r)
	}
	return t, nil
}

// dueOne runs it, of w, unless the pause holds its unit or its facts cannot
// be read; an item whose source or key facts moved is dropped instead.
func (e *Engine) dueOne(ctx context.Context, w workflow.Workflow, it workflow.Item, pause Pause) (result, error) {
	facts, err := e.factsFor(ctx, w, it.Unit)
	if err != nil {
		e.logf("%s: not run, its facts cannot be read: %v", it.ID, err)
		return result{outcome: outcomeNone}, nil
	}
	if holds(pause, w.Slot, facts) {
		return result{outcome: outcomeNone}, nil
	}
	if moved, err := e.sourceMoved(ctx, it); err != nil || moved || keyMoved(w, it, facts) {
		if err == nil {
			err = e.dropMoved(ctx, it)
		}
		return result{outcome: outcomeDropped}, err
	}
	return e.runItem(ctx, it, RunOpts{})
}

// whyMoved drops or refuses an item whose source moved since it was planned.
const whyMoved = "its source moved; planned again on the next sweep"

// sourceMoved says whether it's unit was reported again since the last
// sweep planned it, as the sweep checks before it runs an item; an item
// the last sweep did not plan at a rev never moved.
func (e *Engine) sourceMoved(ctx context.Context, it workflow.Item) (bool, error) {
	r, ok := e.Units.(Revisioner)
	if !ok {
		return false, nil
	}
	e.mu.Lock()
	planned, ok := e.revs[it.ID]
	e.mu.Unlock()
	if !ok {
		return false, nil
	}
	revs, err := r.Revs(ctx, []unit.Unit{it.Unit})
	if err != nil {
		return false, fmt.Errorf("checking sources: %w", err)
	}
	rev, ok := revs[it.Unit]
	return !ok || rev != planned, nil
}

// keyMoved says whether it's key facts changed since it was planned: the
// item stands for facts that no longer hold, and the next evaluation plans
// the current ones with a window of their own.
func keyMoved(w workflow.Workflow, it workflow.Item, facts workflow.Facts) bool {
	return workflow.NewKey(w, it.Unit, facts, it.Why).Value != it.Key.Value
}

// dropMoved discards a pending item without marking its key.
func (e *Engine) dropMoved(ctx context.Context, it workflow.Item) error {
	all, err := e.Ledger.Items(ctx, Filter{Workflow: it.Workflow})
	if err != nil {
		return err
	}
	all = slices.DeleteFunc(all, func(o workflow.Item) bool { return o.ID == it.ID })
	now := e.now()
	e.logf("drop %s: %s", it.ID, whyMoved)
	ev := event(it, workflow.KindItemDropped, it.ID+":"+now.UTC().Format(time.RFC3339Nano), whyMoved, now)
	return e.save(ctx, PlanWrite{Workflows: []string{it.Workflow}, Items: all}, []port.Event{ev})
}
