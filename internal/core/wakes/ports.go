// Package wakes matches the wakes on tasks against time, events and their
// conditions, and emits task.woke for the profile's workflows.
package wakes

import (
	"context"
	"time"

	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/wake"
)

// Store keeps one wake per task.
type Store interface {
	// Put sets the task's wake, replacing the one it had.
	Put(ctx context.Context, w wake.Wake) error
	// Drop removes the task's wake; dropping none is no error.
	Drop(ctx context.Context, id task.ID) error
	// Of is task id's wake, false when it has none.
	Of(ctx context.Context, id task.ID) (wake.Wake, bool, error)
	// Due are the wakes whose Until or Expires is set and not after now,
	// ordered by task id.
	Due(ctx context.Context, now time.Time) ([]wake.Wake, error)
	// Watching are the wakes with On or When set, which events and fact
	// changes match, ordered by task id.
	Watching(ctx context.Context) ([]wake.Wake, error)
}

// Holds says whether wake w's When holds over task t's facts; injected, so
// the core matches a condition without importing the package that evaluates
// the profile's predicates.
type Holds func(ctx context.Context, w wake.Wake, t task.Task) (bool, error)
