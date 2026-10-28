// Package timelinetest holds an in-memory timeline.Reader.
package timelinetest

import (
	"context"
	"slices"
	"sync"

	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/core/timeline"
	"github.com/wloophq/wloop/internal/domain/task"
)

// Reader is an in-memory timeline.Reader over the events Add gives each task.
type Reader struct {
	mu     sync.Mutex
	events map[task.ID][]port.Event
}

var _ timeline.Reader = (*Reader)(nil)

func NewReader() *Reader { return &Reader{events: map[task.ID][]port.Event{}} }

// Add puts evs on task id's timeline.
func (r *Reader) Add(id task.ID, evs ...port.Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events[id] = append(r.events[id], evs...)
}

func (r *Reader) Of(_ context.Context, id task.ID, f timeline.Filter) ([]port.Event, error) {
	r.mu.Lock()
	evs := slices.Clone(r.events[id])
	r.mu.Unlock()
	slices.SortStableFunc(evs, func(a, b port.Event) int { return b.At.Compare(a.At) })
	out := []port.Event{}
	for _, ev := range evs {
		if len(f.Kinds) > 0 && !slices.Contains(f.Kinds, ev.Kind) || !f.Before.IsZero() && !ev.At.Before(f.Before) {
			continue
		}
		if f.Limit > 0 && len(out) == f.Limit {
			break
		}
		out = append(out, ev)
	}
	return out, nil
}
