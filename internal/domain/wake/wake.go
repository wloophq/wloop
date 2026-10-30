// Package wake is a task's wait for a time, an event or a condition, which
// the host matches and turns into a task.woke event a profile workflow
// reacts to.
package wake

import (
	"time"

	"github.com/wloophq/wloop/internal/domain/task"
)

// EventWoke is the kind of the event a matched wake emits on its task.
const EventWoke = "task.woke"

// Wake is a task's one wake; a zero field matches nothing.
//
// Until wakes the task at a time. On are the event kinds on the task or its
// links that wake it, narrowed to the events of Actors when set. When is a
// predicate over the task's facts that wakes it once it holds. Expires drops
// the wake unmatched; zero never expires. Return is the state the profile's
// wake workflow moves the task back to; "" leaves it to the workflow.
type Wake struct {
	Task    task.ID
	Until   time.Time
	On      []string
	Actors  []string
	When    string
	Expires time.Time
	Return  string
}

// OnActivity is the kind in On that a person's new activity on a unit the
// task holds matches.
const OnActivity = "activity"
