package workflow

import (
	"time"

	"github.com/wloophq/wloop/internal/domain/workday"
)

// TemporalKind is how a Temporal fact reads the clock.
type TemporalKind int

const (
	// TemporalSince is the duration from Time to now.
	TemporalSince TemporalKind = iota + 1
	// TemporalAt is true from Time on.
	TemporalAt
	// TemporalAfter is true once more than Threshold has passed since
	// Time, counted in working days when WorkingDays.
	TemporalAfter
)

// Temporal is a fact whose value changes with the clock, which a source
// reports instead of a value it would have to report again as time passes.
// The engine resolves it with its own clock whenever it reads the facts.
// A Temporal whose Time is zero resolves to its type's zero and has no
// deadline.
type Temporal struct {
	Kind        TemporalKind
	Time        time.Time
	Threshold   time.Duration
	WorkingDays bool
}

// Value is t at now: a time.Duration for TemporalSince, a bool otherwise.
func (t Temporal) Value(now time.Time) any {
	switch t.Kind {
	case TemporalSince:
		if t.Time.IsZero() {
			return time.Duration(0)
		}
		return now.Sub(t.Time)
	case TemporalAt:
		return !t.Time.IsZero() && !now.Before(t.Time)
	case TemporalAfter:
		if t.Time.IsZero() {
			return false
		}
		if t.WorkingDays {
			return workday.Between(t.Time, now) > t.Threshold
		}
		return now.Sub(t.Time) > t.Threshold
	}
	return nil
}

// Deadline is the instant after now at which t's value next changes;
// false when it never does: a TemporalSince changes continuously, and the
// other kinds change once.
func (t Temporal) Deadline(now time.Time) (time.Time, bool) {
	if t.Time.IsZero() {
		return time.Time{}, false
	}
	var at time.Time
	switch t.Kind {
	case TemporalAt:
		at = t.Time
	case TemporalAfter:
		// Durations are whole nanoseconds, so "more than Threshold" first
		// holds one nanosecond past it.
		if t.WorkingDays {
			at = workday.Add(t.Time, t.Threshold+time.Nanosecond)
		} else {
			at = t.Time.Add(t.Threshold + time.Nanosecond)
		}
	default:
		return time.Time{}, false
	}
	return at, at.After(now)
}

// Resolve is f with every Temporal resolved at now, and the earliest
// deadline among them after now; zero when none has one. f is returned as
// it is when it holds no Temporal, and never written.
func Resolve(f Facts, now time.Time) (Facts, time.Time) {
	var out Facts
	var due time.Time
	for name, v := range f {
		t, ok := v.(Temporal)
		if !ok {
			continue
		}
		if out == nil {
			out = make(Facts, len(f))
			for k, v := range f {
				out[k] = v
			}
		}
		out[name] = t.Value(now)
		if at, ok := t.Deadline(now); ok && (due.IsZero() || at.Before(due)) {
			due = at
		}
	}
	if out == nil {
		return f, time.Time{}
	}
	return out, due
}
