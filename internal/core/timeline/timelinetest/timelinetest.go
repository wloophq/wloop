// Package timelinetest holds an in-memory timeline.Reader.
package timelinetest

import (
	"cmp"
	"context"
	"slices"
	"sync"

	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/core/timeline"
	"github.com/wloophq/wloop/internal/domain/task"
)

// Reader is an in-memory timeline.Reader over the events Add gives each
// task, numbered in the order they were added.
type Reader struct {
	mu     sync.Mutex
	seq    int64
	events map[task.ID][]timeline.Event
}

var _ timeline.Reader = (*Reader)(nil)

func NewReader() *Reader { return &Reader{events: map[task.ID][]timeline.Event{}} }

// Add puts evs on task id's timeline.
func (r *Reader) Add(id task.ID, evs ...port.Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, ev := range evs {
		r.seq++
		r.events[id] = append(r.events[id], timeline.Event{Seq: r.seq, Event: ev})
	}
}

func (r *Reader) Of(_ context.Context, id task.ID, f timeline.Filter) ([]timeline.Event, error) {
	r.mu.Lock()
	evs := slices.Clone(r.events[id])
	r.mu.Unlock()
	slices.SortFunc(evs, func(a, b timeline.Event) int { return cmp.Compare(b.Seq, a.Seq) })
	out := []timeline.Event{}
	for _, ev := range evs {
		if len(f.Kinds) > 0 && !slices.Contains(f.Kinds, ev.Kind) || f.Before > 0 && ev.Seq >= f.Before {
			continue
		}
		if f.Limit > 0 && len(out) == f.Limit {
			break
		}
		out = append(out, ev)
	}
	return out, nil
}
