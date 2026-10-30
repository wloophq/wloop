package automation

import (
	"context"
	"fmt"
	"time"

	"github.com/wloophq/wloop/internal/domain/workflow"
)

// batch is what one workflow's trigger brings to an evaluation.
type batch struct {
	w workflow.Workflow
	// events are the unhandled events of the workflow's provider its trigger fires on (TriggerEvent): of its
	// actors, in a chain shorter than its depth that it is not in yet.
	events []workflow.Event
	// fired is the schedule firing the batch handles; zero when none (TriggerSchedule).
	fired time.Time
	// advance, cursor and lastFire are what Sweep moves the workflow's
	// cursor to once the plan is saved.
	advance  bool
	cursor   int64
	lastFire time.Time
	from     int64
}

// batches reads each workflow's trigger. A workflow with no cursor starts at
// the head of the events, or at now for a schedule: apply stores that start,
// a pure evaluation only assumes it. A workflow whose trigger cannot be read
// is left out with an error.
func (e *Engine) batches(ctx context.Context, ws []workflow.Workflow, now time.Time, apply bool) ([]batch, []string) {
	var out []batch
	var errs []string
	for _, w := range ws {
		b, err := e.batch(ctx, w, now, apply)
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", w.Name, err))
			continue
		}
		out = append(out, b)
	}
	return out, errs
}

func (e *Engine) batch(ctx context.Context, w workflow.Workflow, now time.Time, apply bool) (batch, error) {
	b := batch{w: w}
	switch w.Trigger.Kind {
	case workflow.TriggerSweep:
		return b, nil
	case workflow.TriggerEvent, workflow.TriggerSchedule:
	default:
		return b, fmt.Errorf("unknown trigger %q", w.Trigger.Kind)
	}
	if e.Events == nil {
		return b, fmt.Errorf("no event store for a %s trigger", w.Trigger.Kind)
	}
	cur, lastFire, ok, err := e.Events.Cursor(ctx, w.Name)
	if err != nil {
		return b, err
	}
	if !ok {
		if w.Trigger.Kind == workflow.TriggerEvent {
			if cur, err = e.Events.Head(ctx); err != nil {
				return b, err
			}
		}
		if w.Trigger.Kind == workflow.TriggerSchedule {
			lastFire = now
		}
		if apply {
			if err := e.Events.Advance(ctx, w.Name, cur, lastFire); err != nil {
				return b, err
			}
		}
		b.cursor, b.lastFire = cur, lastFire
		return b, nil
	}
	b.cursor, b.lastFire, b.from = cur, lastFire, cur
	if w.Trigger.Kind == workflow.TriggerSchedule {
		if w.Trigger.Schedule == nil {
			return b, fmt.Errorf("a schedule trigger without a schedule")
		}
		fire := w.Trigger.Schedule.Prev(now)
		if !fire.IsZero() && fire.After(lastFire) {
			if sameDay(fire, now) {
				b.fired = fire
			}
			b.advance, b.lastFire = true, fire
		}
		return b, nil
	}
	evs, err := e.Events.Since(ctx, cur, w.Trigger.On)
	if err != nil {
		return b, err
	}
	for _, ev := range evs {
		if ev.ID > b.cursor {
			b.advance, b.cursor = true, ev.ID
		}
		if ev.Unit.Provider == w.Provider && w.Trigger.Fires(w.Name, ev) {
			b.events = append(b.events, ev)
		}
	}
	return b, nil
}

// hold keeps the cursor before the event id, which could not be decided and
// is read again on the next sweep.
func (b *batch) hold(id int64) {
	if id-1 < b.cursor {
		b.cursor = id - 1
		b.advance = b.cursor != b.from
	}
}

// sameDay says whether a falls on b's local date.
func sameDay(a, b time.Time) bool {
	ay, am, ad := a.In(b.Location()).Date()
	by, bm, bd := b.Date()
	return ay == by && am == bm && ad == bd
}

func startOfDay(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, t.Location())
}

func triggerFacts(why workflow.Why) workflow.Facts {
	f := workflow.Facts{"trigger.kind": string(why.Trigger)}
	if ev := why.Event; ev != nil {
		f["trigger.event.kind"] = ev.Kind
		f["trigger.event.key"] = ev.Key
		f["trigger.event.id"] = ev.ID
		f["trigger.event.data"] = ev.Data
	}
	if !why.FiredAt.IsZero() {
		f["trigger.fired_at"] = why.FiredAt
	}
	return f
}
