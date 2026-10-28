// Package timeline reads a task's timeline: its own events and those of
// the units linked to it, every one kept forever.
package timeline

import (
	"context"

	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/domain/task"
)

// Reader reads timelines.
type Reader interface {
	// Of are the events of task id and of its linked units that f keeps,
	// newest first (by Seq); empty for a task with none.
	Of(ctx context.Context, id task.ID, f Filter) ([]Event, error)
}

// Event is an event of a timeline at Seq, its row in the event log, which
// grows with every event stored and orders and pages the timeline.
type Event struct {
	Seq int64
	port.Event
}

// Filter narrows a timeline: Kinds keeps those event kinds (every kind when
// empty), Before the events whose Seq is lower (all when 0), Limit at most
// that many (all when 0).
type Filter struct {
	Kinds  []string
	Before int64
	Limit  int
}
