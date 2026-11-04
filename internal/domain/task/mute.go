package task

import (
	"slices"
	"time"

	"github.com/wloophq/wloop/internal/domain/items"
)

// Activity marks Changed the tasks of f.Unit parked on someone else when f.LastHuman is after what the user
// saw (SeenAt, or Created before any): a deferred task, or an open one out of the inbox that has an owner or
// holds an item of its that waits on someone but me. It moves no task: a task never returns to the inbox on
// activity, and a wake on activity is the wakes' to match.
func Activity(f UnitFacts, its []items.Item, me Me, have []Task, now time.Time) []Change {
	var out []Change
	for _, e := range have {
		if !parked(f, e, its, me) || e.Changed || !ActiveSince(f, e) {
			continue
		}
		e.Changed, e.Updated = true, now
		out = append(out, Change{Op: OpUpdate, Task: e, Why: "activity on " + f.Unit.String()})
	}
	return out
}

func parked(f UnitFacts, e Task, its []items.Item, me Me) bool {
	if e.Category == CategoryDeferred {
		return true
	}
	if !e.Open() || e.Category == CategoryInbox {
		return false
	}
	if !e.Prop(PropOwner).IsZero() {
		return true
	}
	return slices.ContainsFunc(its, func(it items.Item) bool {
		return it.State == itemOpen && slices.Contains(e.Items, refOf(f, it)) &&
			slices.ContainsFunc(it.WaitingOn, func(p items.Person) bool { return !isMe(p, me) })
	})
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
