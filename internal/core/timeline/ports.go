// Package timeline reads a task's timeline: its own events and those of
// the units linked to it, every one kept forever.
package timeline

import (
	"context"
	"time"

	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/domain/task"
)

// Reader reads timelines.
type Reader interface {
	// Of are the events of task id and of its linked units that f keeps,
	// newest first; empty for a task with none.
	Of(ctx context.Context, id task.ID, f Filter) ([]port.Event, error)
}

// Filter narrows a timeline: Kinds keeps those event kinds (every kind when
// empty), Before the events strictly before it (all when zero), Limit at most
// that many (all when 0).
type Filter struct {
	Kinds  []string
	Before time.Time
	Limit  int
}
