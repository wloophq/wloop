// Package wakestest holds an in-memory wakes.Store and the suite every wakes
// store passes.
package wakestest

import (
	"cmp"
	"context"
	"maps"
	"reflect"
	"slices"
	"sync"
	"testing"
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
	s.wakes[w.Task] = clone(w)
	return nil
}

func (s *Store) Drop(_ context.Context, id task.ID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.wakes, id)
	return nil
}

func (s *Store) Of(_ context.Context, id task.ID) (wake.Wake, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	w, ok := s.wakes[id]
	return clone(w), ok, nil
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
			out = append(out, clone(w))
		}
	}
	return out
}

func clone(w wake.Wake) wake.Wake {
	w.On, w.Actors = slices.Clone(w.On), slices.Clone(w.Actors)
	return w
}

// Epoch is a time StoreContract's wakes are set around.
var Epoch = time.Date(2026, 10, 28, 9, 0, 0, 0, time.UTC)

// StoreContract is the suite every wakes store passes; open returns an empty
// store holding the tasks t-01, t-02 and t-03.
func StoreContract(t *testing.T, open func(t *testing.T) wakes.Store) {
	ctx := context.Background()
	t.Run("put reads back", func(t *testing.T) {
		s := open(t)
		w := wake.Wake{Task: "t-01", Until: Epoch, On: []string{"comment"}, Actors: []string{"slack:U01"}, When: "task.owed_by != 'me'",
			Expires: Epoch.Add(time.Hour), Return: "next"}
		if err := s.Put(ctx, w); err != nil {
			t.Fatal(err)
		}
		got, ok, err := s.Of(ctx, "t-01")
		if err != nil || !ok || !reflect.DeepEqual(norm(got), norm(w)) {
			t.Fatalf("of %+v %v %v", got, ok, err)
		}
	})
	t.Run("a task with none has none", func(t *testing.T) {
		s := open(t)
		if _, ok, err := s.Of(ctx, "t-01"); ok || err != nil {
			t.Fatalf("of: %v %v", ok, err)
		}
	})
	t.Run("put replaces and drop removes", func(t *testing.T) {
		s := open(t)
		_ = s.Put(ctx, wake.Wake{Task: "t-01", Until: Epoch})
		if err := s.Put(ctx, wake.Wake{Task: "t-01", When: "true"}); err != nil {
			t.Fatal(err)
		}
		if got, _, _ := s.Of(ctx, "t-01"); !got.Until.IsZero() || got.When != "true" {
			t.Fatalf("replaced %+v", got)
		}
		if err := s.Drop(ctx, "t-01"); err != nil {
			t.Fatal(err)
		}
		if err := s.Drop(ctx, "t-01"); err != nil {
			t.Fatalf("a second drop: %v", err)
		}
		if _, ok, _ := s.Of(ctx, "t-01"); ok {
			t.Fatal("dropped wake still read")
		}
	})
	t.Run("due is at exactly now, by until or expiry", func(t *testing.T) {
		s := open(t)
		_ = s.Put(ctx, wake.Wake{Task: "t-03", Until: Epoch})
		_ = s.Put(ctx, wake.Wake{Task: "t-01", On: []string{"comment"}, Expires: Epoch})
		_ = s.Put(ctx, wake.Wake{Task: "t-02", Until: Epoch.Add(time.Second)})
		due, err := s.Due(ctx, Epoch)
		if err != nil || !slices.Equal(tasksOf(due), []task.ID{"t-01", "t-03"}) {
			t.Fatalf("due %v %v", tasksOf(due), err)
		}
	})
	t.Run("watching are the event and condition wakes", func(t *testing.T) {
		s := open(t)
		_ = s.Put(ctx, wake.Wake{Task: "t-03", When: "true"})
		_ = s.Put(ctx, wake.Wake{Task: "t-02", Until: Epoch})
		_ = s.Put(ctx, wake.Wake{Task: "t-01", On: []string{"comment"}})
		got, err := s.Watching(ctx)
		if err != nil || !slices.Equal(tasksOf(got), []task.ID{"t-01", "t-03"}) {
			t.Fatalf("watching %v %v", tasksOf(got), err)
		}
	})
}

func norm(w wake.Wake) wake.Wake {
	w.Until, w.Expires = w.Until.UTC(), w.Expires.UTC()
	if len(w.On) == 0 {
		w.On = nil
	}
	if len(w.Actors) == 0 {
		w.Actors = nil
	}
	return w
}

func tasksOf(ws []wake.Wake) []task.ID {
	out := []task.ID{}
	for _, w := range ws {
		out = append(out, w.Task)
	}
	return out
}
