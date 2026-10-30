package draft

import (
	"time"

	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
)

// SettledAt is the newest UpdatedAt of the drafts of ds that workflow
// proposed on subject and that settled, sent or dropped (withdrawn ones
// included; a failed draft may still be approved again); zero when none. A draft is on a task subject when it serves that task, and on
// any other subject when it is on that unit.
func SettledAt(ds []Draft, workflow string, subject unit.Unit) time.Time {
	var out time.Time
	for _, d := range ds {
		if d.Origin.Workflow != workflow || (d.State != Sent && d.State != Dropped) || !on(d, subject) {
			continue
		}
		if d.UpdatedAt.After(out) {
			out = d.UpdatedAt
		}
	}
	return out
}

func on(d Draft, subject unit.Unit) bool {
	return d.Unit == subject || d.Task != "" && task.UnitOf(task.ID(d.Task)) == subject
}
