package automation

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/workflow"
)

// tally counts what a sweep did.
type tally struct {
	ran, failed, refused, deferred, transient, dropped int
}

func (t *tally) add(r result) {
	switch r.outcome {
	case outcomeDone:
		t.ran++
	case outcomeRefused:
		t.refused++
	case outcomeDeferred:
		t.deferred++
	case outcomeTransient:
		t.transient++
	case outcomeFailed:
		t.failed++
	}
}

// Sweep implements daemon.Sweeper. Without the loop lock it checks the
// manager is reachable, refreshes the facts providers, evaluates every
// trigger, saves the plan, advances the event cursors and drops the run
// items whose source moved. Under the lock it drops the run items whose unit
// was noted after the plan's generation and no longer match, notes the
// refusals, runs the run items and the due pending ones, withdraws the
// drafts the workflows no longer call for, and settles.
func (e *Engine) Sweep(ctx context.Context, loop sync.Locker) (summary, skipped string, err error) {
	if e.Reachable != nil {
		if err := e.Reachable(ctx); err != nil {
			return "", "the workspace manager is unreachable: " + err.Error(), nil
		}
	}
	if r, ok := e.Units.(Refresher); ok {
		if err := r.Refresh(ctx); err != nil {
			return "", "", fmt.Errorf("refreshing facts: %w", err)
		}
	}
	ev, err := e.evaluate(ctx, "", true)
	if err != nil {
		return "", "", err
	}
	if s, ok := e.Units.(Seener); ok {
		e.mu.Lock()
		e.seen = s.Seen()
		e.mu.Unlock()
	}
	for _, msg := range ev.plan.Errors {
		e.logf("%s", msg)
	}
	gone, err := e.gone(ctx)
	if err != nil {
		return "", "", err
	}
	write := PlanWrite{Workflows: append(slices.Clone(ev.workflows), gone...), Items: ev.plan.Items}
	if err := e.save(ctx, write, append(plannedEvents(ev, e.now()), ev.passed...)); err != nil {
		return "", "", err
	}
	for _, b := range ev.batches {
		if !b.advance {
			continue
		}
		if err := e.Events.Advance(ctx, b.w.Name, b.cursor, b.lastFire); err != nil {
			return "", "", fmt.Errorf("advancing %s: %w", b.w.Name, err)
		}
	}
	for _, d := range ev.decided {
		e.events.decide(d.workflow, d.id)
	}
	var t tally
	runs, err := e.fresh(ctx, ev.items, &t)
	if err != nil {
		return "", "", err
	}

	loop.Lock()
	defer loop.Unlock()
	pause, err := e.pause(ctx)
	if err != nil {
		return "", "", err
	}
	runs = e.revalidate(ctx, ev, runs, &t)
	runs = e.drop(runs, &t, func(pl *planned) string {
		if holds(pause, pl.w.Slot, pl.facts) {
			return "paused since it was planned"
		}
		return ""
	})
	st, err := e.loadState(ctx)
	if err != nil {
		return "", "", err
	}
	defer func() {
		if serr := e.saveState(ctx, st); serr != nil {
			err = errors.Join(err, serr)
		}
	}()
	for _, pl := range ev.items {
		if pl.refusedStep < 0 || pl.w.Observe {
			continue
		}
		if err := e.Steps.Note(ctx, pl.w.Steps[pl.refusedStep], pl.item.Unit, pl.item.Why, pl.refusal); err != nil {
			e.logf("%s: noting %q: %v", pl.item.ID, pl.refusal, err)
		}
	}
	for _, pl := range runs {
		why, err := e.settled(ctx, pl.item)
		if err != nil {
			return "", "", err
		}
		if why != "" {
			e.logf("drop %s: %s", pl.item.ID, why)
			t.dropped++
			continue
		}
		o := RunOpts{}
		if pl.guided && e.Guidance != nil {
			if o.Guidance, err = e.Guidance.Take(ctx, pl.item.Unit); err != nil {
				return "", "", err
			}
		}
		r, err := e.runItem(ctx, pl.item, o)
		if o.Guidance != "" && r.outcome != outcomeDone && r.outcome != outcomeDeferred {
			if kerr := e.Guidance.Keep(ctx, pl.item.Unit, o.Guidance); kerr != nil {
				e.logf("%s: keeping the guidance: %v", pl.item.ID, kerr)
			}
		}
		if err != nil {
			return "", "", err
		}
		t.add(r)
	}
	due, err := e.dueRun(ctx, pause)
	if err != nil {
		return "", "", err
	}
	t.ran, t.failed, t.refused, t.deferred, t.transient = t.ran+due.ran, t.failed+due.failed,
		t.refused+due.refused, t.deferred+due.deferred, t.transient+due.transient
	if err := e.withdraw(ctx); err != nil {
		e.logf("withdrawing drafts: %v", err)
	}
	if s, ok := e.Units.(Settler); ok {
		if err := s.Settle(ctx, st); err != nil {
			return "", "", fmt.Errorf("settling: %w", err)
		}
	}
	if err := e.schedule(ctx, ev); err != nil {
		return "", "", err
	}
	summary = summarize(ev, t)
	if t.failed > 0 && t.ran+t.refused+t.deferred == 0 {
		return summary, "", fmt.Errorf("%d item(s) failed", t.failed)
	}
	return summary, "", nil
}

func (e *Engine) pause(ctx context.Context) (Pause, error) {
	if e.Pause == nil {
		return Pause{}, nil
	}
	p, err := e.Pause(ctx)
	if err != nil {
		return Pause{}, fmt.Errorf("reading the pause: %w", err)
	}
	return p, nil
}

// gone are the workflows the ledger holds items of that are no longer
// loaded or enabled: their items leave the plan.
func (e *Engine) gone(ctx context.Context) ([]string, error) {
	stored, err := e.Ledger.Items(ctx, Filter{})
	if err != nil {
		return nil, err
	}
	live := e.live()
	var out []string
	for _, it := range stored {
		if _, ok := live[it.Workflow]; !ok && !slices.Contains(out, it.Workflow) {
			out = append(out, it.Workflow)
		}
	}
	return out, nil
}

func (e *Engine) live() map[string]workflow.Workflow {
	live := map[string]workflow.Workflow{}
	for _, w := range e.workflows() {
		live[w.Name] = w
	}
	return live
}

// fresh are the plan's run items whose source did not move since their facts were read.
func (e *Engine) fresh(ctx context.Context, items []*planned, t *tally) ([]*planned, error) {
	var runs []*planned
	for _, pl := range items {
		if pl.item.Decision == workflow.DecisionRun && !pl.item.Observe && !pl.carried {
			runs = append(runs, pl)
		}
	}
	f, ok := e.Units.(Freshener)
	if !ok || len(runs) == 0 {
		return runs, nil
	}
	units := make([]unit.Unit, 0, len(runs))
	for _, pl := range runs {
		if !slices.Contains(units, pl.item.Unit) {
			units = append(units, pl.item.Unit)
		}
	}
	moved, err := f.Moved(ctx, units)
	if err != nil {
		return nil, fmt.Errorf("checking sources: %w", err)
	}
	return e.drop(runs, t, func(pl *planned) string {
		if slices.Contains(moved, pl.item.Unit) {
			return "its source moved; planned again on the next sweep"
		}
		return ""
	}), nil
}

// revalidate drops the run items whose unit was noted on Revalidate after
// the plan's generation and no longer matches on fresh facts under the same key.
func (e *Engine) revalidate(ctx context.Context, ev *evaluation, runs []*planned, t *tally) []*planned {
	if e.Changes == nil || len(runs) == 0 {
		return runs
	}
	ch := e.Changes.Since(ev.plan.Gen, Revalidate)
	if ch.Empty() {
		return runs
	}
	now := e.now()
	var config workflow.Facts
	if e.Config != nil {
		config = e.Config()
	}
	return e.drop(runs, t, func(pl *planned) string {
		if !ch.All && !slices.Contains(ch.Units, pl.item.Unit) {
			return ""
		}
		facts, err := e.factsFor(ctx, pl.w.Name, pl.item.Unit)
		if err != nil {
			return "its facts cannot be read: " + err.Error()
		}
		vars := workflow.Vars{Facts: facts, Trigger: triggerFacts(pl.item.Why), Config: config, Now: now}
		if readsMarks(pl.w) {
			if vars.Marks, err = e.unitMarks(ctx, pl.item.Unit); err != nil {
				return "its marks cannot be read: " + err.Error()
			}
		}
		if ok, err := match(pl.w.When, vars); err != nil || !ok {
			return "it no longer matches since it was planned"
		}
		if workflow.NewKey(pl.w, pl.item.Unit, facts, pl.item.Why.FiredAt) != pl.item.Key {
			return "its unit changed since it was planned"
		}
		return ""
	})
}

// settled says why a planned run must not run: after the plan was saved,
// an Act under the lock ran, skipped or dismissed its key, took it out of
// the plan or left it with another decision.
func (e *Engine) settled(ctx context.Context, it workflow.Item) (string, error) {
	run, ok, err := e.Ledger.Run(ctx, it.Key)
	if err != nil {
		return "", err
	}
	if ok && (run.Status == workflow.RunDone || run.Status == workflow.RunDismissed) {
		return "it was " + string(run.Status) + " since it was planned", nil
	}
	stored, err := e.Ledger.Items(ctx, Filter{ID: it.ID})
	if err != nil {
		return "", err
	}
	switch {
	case len(stored) == 0:
		return "it left the plan since it was planned", nil
	case stored[0].Decision != workflow.DecisionRun:
		return "it was acted on since it was planned", nil
	}
	return "", nil
}

func (e *Engine) drop(runs []*planned, t *tally, why func(*planned) string) []*planned {
	kept := runs[:0:0]
	for _, pl := range runs {
		if w := why(pl); w != "" {
			e.logf("drop %s: %s", pl.item.ID, w)
			t.dropped++
			continue
		}
		kept = append(kept, pl)
	}
	return kept
}

// schedule keeps the earliest instant a decision of this sweep changes with
// no input changing.
func (e *Engine) schedule(ctx context.Context, ev *evaluation) error {
	now := e.now()
	var due time.Time
	for _, pl := range ev.items {
		if pl.retryAt.After(now) {
			due = earliest(due, pl.retryAt)
		}
	}
	pending, err := e.Ledger.Items(ctx, Filter{Decisions: []workflow.Decision{workflow.DecisionPending}})
	if err != nil {
		return err
	}
	for _, it := range pending {
		if it.Default == workflow.OutcomeRun && !it.Observe && it.Deadline.After(now) {
			due = earliest(due, it.Deadline)
		}
	}
	for _, w := range e.workflows() {
		if w.Trigger.Kind == workflow.TriggerSchedule && w.Trigger.Schedule != nil {
			due = earliest(due, w.Trigger.Schedule.Next(now))
		}
	}
	e.mu.Lock()
	e.due = due
	e.mu.Unlock()
	return nil
}

// plannedEvents are the workflow_planned events of the items whose decision
// is new or changed, so a plan that did not change stores nothing.
func plannedEvents(ev *evaluation, at time.Time) []port.Event {
	var evs []port.Event
	for _, pl := range ev.items {
		if pl.prevDecision != pl.item.Decision {
			evs = append(evs, event(pl.item, workflow.KindPlanned, pl.item.ID+":"+string(pl.item.Decision), pl.item.Reason, at))
		}
	}
	return evs
}

func summarize(ev *evaluation, t tally) string {
	n := map[workflow.Decision]int{}
	for _, it := range ev.plan.Items {
		n[it.Decision]++
	}
	s := fmt.Sprintf("%d item(s): %d ran, %d failed, %d refused, %d pending, %d blocked, %d skipped",
		len(ev.plan.Items), t.ran, t.failed, t.refused, n[workflow.DecisionPending]+t.deferred,
		n[workflow.DecisionBlocked], n[workflow.DecisionSkipped])
	if t.dropped > 0 {
		s += fmt.Sprintf(", %d dropped", t.dropped)
	}
	if t.transient > 0 {
		s += fmt.Sprintf(", %d deferred to the next sweep", t.transient)
	}
	if len(ev.plan.Errors) > 0 {
		s += fmt.Sprintf(", %d error(s)", len(ev.plan.Errors))
	}
	return s
}
