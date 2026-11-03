package draft

import (
	"slices"
	"time"

	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
)

// SettledStates are the states of a settled draft.
var SettledStates = []State{Sent, Dropped}

// SettledAt is the newest UpdatedAt of the drafts of ds that workflow
// proposed on subject and that settled, sent or dropped (withdrawn ones
// included; a failed draft may still be approved again); zero when none. A draft is on a task subject when it serves that task, and on
// any other subject when it is on that unit.
func SettledAt(ds []Draft, workflow string, subject unit.Unit) time.Time {
	var out time.Time
	for _, d := range ds {
		if d.Origin.Workflow != workflow || !slices.Contains(SettledStates, d.State) || !on(d, subject) {
			continue
		}
		if d.UpdatedAt.After(out) {
			out = d.UpdatedAt
		}
	}
	return out
}

func on(d Draft, subject unit.Unit) bool {
	return slices.Contains(Subjects(d), subject)
}

// Subjects are the subjects d is on: its unit and, when it serves one, its task.
func Subjects(d Draft) []unit.Unit {
	if d.Task == "" {
		return []unit.Unit{d.Unit}
	}
	return []unit.Unit{d.Unit, task.UnitOf(task.ID(d.Task))}
}
