// Package wakestest holds an in-memory wakes.Store.
package wakestest

import (
	"cmp"
	"context"
	"maps"
	"slices"
	"sync"
	"time"

	"github.com/wloophq/wloop/internal/core/wakes"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/wake"
)

// Store is an in-memory wakes.Store.
type Store struct {
	mu    sync.Mutex
	wakes map[task.ID]wake.Wake
}

var _ wakes.Store = (*Store)(nil)

func NewStore() *Store { return &Store{wakes: map[task.ID]wake.Wake{}} }

func (s *Store) Put(_ context.Context, w wake.Wake) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.wakes[w.Task] = w
	return nil
}

func (s *Store) Drop(_ context.Context, id task.ID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.wakes, id)
	return nil
}

func (s *Store) Due(_ context.Context, now time.Time) ([]wake.Wake, error) {
	return s.filter(func(w wake.Wake) bool {
		return !w.Until.IsZero() && !w.Until.After(now) || !w.Expires.IsZero() && !w.Expires.After(now)
	}), nil
}

func (s *Store) Watching(context.Context) ([]wake.Wake, error) {
	return s.filter(func(w wake.Wake) bool { return len(w.On) > 0 || w.When != "" }), nil
}

func (s *Store) filter(keep func(wake.Wake) bool) []wake.Wake {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []wake.Wake{}
	for _, w := range slices.SortedFunc(maps.Values(s.wakes), func(a, b wake.Wake) int { return cmp.Compare(a.Task, b.Task) }) {
		if keep(w) {
			out = append(out, w)
		}
	}
	return out
}
