package task

import "time"

// Activity marks Changed the deferred and waiting tasks of f.Unit when f.LastHuman is after what the user saw
// (SeenAt, or Created before any). It moves no task: a task never returns to the inbox on activity, and a
// wake on activity is the wakes' to match.
func Activity(f UnitFacts, have []Task, now time.Time) []Change {
	var out []Change
	for _, e := range have {
		if e.Category != CategoryDeferred && e.Category != CategoryWaiting {
			continue
		}
		if e.Changed || !ActiveSince(f, e) {
			continue
		}
		e.Changed, e.Updated = true, now
		out = append(out, Change{Op: OpUpdate, Task: e, Why: "activity on " + f.Unit.String()})
	}
	return out
}

// ActiveSince says whether a person's activity on f.Unit came after the user saw task e, or after e was made
// when they never did.
func ActiveSince(f UnitFacts, e Task) bool { return f.LastHuman.After(later(e.SeenAt, e.Created)) }

func later(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}
