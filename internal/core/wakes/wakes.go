package wakes

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/wloophq/wloop/internal/core/port"
	coretasks "github.com/wloophq/wloop/internal/core/tasks"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/wake"
)

// Tasks are the tasks the wakes are on, and where a matched wake's
// task.woke event is recorded. Task fails with tasks.ErrNoTask for a task
// it does not hold.
type Tasks interface {
	Task(ctx context.Context, id task.ID) (task.Task, error)
	Apply(ctx context.Context, cs []task.Change, evs ...port.Event) error
}

// Deps are what the wakes run on.
type Deps struct {
	Store Store
	Tasks Tasks
	// Holds says whether a wake's When holds; nil: no condition holds.
	Holds Holds
	Clock port.Clock
}

// Service matches the wakes: a wake whose Until passed, whose When holds or
// one of whose On an event matches wakes its task; one whose Expires passed
// first is dropped unmatched. Waking records task.woke on the task, with the
// wake's Return as its data, and spends the wake: it keeps only its Return,
// which matches nothing, for the profile's wake workflow to read.
type Service struct {
	d  Deps
	mu sync.Mutex
}

// New is the wakes over d.
func New(d Deps) *Service { return &Service{d: d} }

// Sweep wakes or drops the wakes due at now and wakes those whose
// condition holds. A wake whose task is gone or closed is dropped.
func (s *Service) Sweep(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.d.Clock.Now()
	due, err := s.d.Store.Due(ctx, now)
	if err != nil {
		return err
	}
	for _, w := range due {
		if !w.Until.IsZero() && !w.Until.After(now) {
			err = errors.Join(err, s.woke(ctx, w, "until "+w.Until.UTC().Format(time.RFC3339)))
			continue
		}
		err = errors.Join(err, s.d.Store.Drop(ctx, w.Task))
	}
	watching, werr := s.d.Store.Watching(ctx)
	if werr != nil {
		return errors.Join(err, werr)
	}
	for _, w := range watching {
		if w.When == "" || s.d.Holds == nil {
			continue
		}
		err = errors.Join(err, s.match(ctx, w))
	}
	return err
}

func (s *Service) match(ctx context.Context, w wake.Wake) error {
	t, ok, err := s.live(ctx, w)
	if err != nil || !ok {
		return err
	}
	holds, err := s.d.Holds(ctx, w, t)
	if err != nil {
		return fmt.Errorf("the wake of %s: %w", w.Task, err)
	}
	if !holds {
		return nil
	}
	return s.woke(ctx, w, "when "+w.When)
}

// Event matches the wake of task id against an event of kind by actor on
// the task or a unit it holds.
func (s *Service) Event(ctx context.Context, id task.ID, kind, actor string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	w, ok, err := s.d.Store.Of(ctx, id)
	if err != nil || !ok || !slices.Contains(w.On, kind) {
		return err
	}
	if len(w.Actors) > 0 && !slices.Contains(w.Actors, actor) {
		return nil
	}
	if !w.Expires.IsZero() && !w.Expires.After(s.d.Clock.Now()) {
		return s.d.Store.Drop(ctx, id)
	}
	if _, ok, err := s.live(ctx, w); err != nil || !ok {
		return err
	}
	return s.woke(ctx, w, "on "+kind)
}

// live is w's task while it is open; a wake of a task gone or closed is
// dropped.
func (s *Service) live(ctx context.Context, w wake.Wake) (task.Task, bool, error) {
	t, err := s.d.Tasks.Task(ctx, w.Task)
	switch {
	case errors.Is(err, coretasks.ErrNoTask):
		return task.Task{}, false, s.d.Store.Drop(ctx, w.Task)
	case err != nil:
		return task.Task{}, false, err
	case !t.Open():
		return task.Task{}, false, s.d.Store.Drop(ctx, w.Task)
	}
	return t, true, nil
}

// woke records task.woke on w's task and spends w.
func (s *Service) woke(ctx context.Context, w wake.Wake, why string) error {
	if _, ok, err := s.live(ctx, w); err != nil || !ok {
		return err
	}
	ev := port.Event{Unit: task.UnitOf(w.Task), Kind: wake.EventWoke, Actor: "wake", Data: w.Return, At: s.d.Clock.Now()}
	if err := s.d.Tasks.Apply(ctx, nil, ev); err != nil {
		return fmt.Errorf("the wake of %s, %s: %w", w.Task, why, err)
	}
	return s.d.Store.Put(ctx, wake.Wake{Task: w.Task, Return: w.Return})
}

// Next is the earliest Until or Expires of the wakes after now; zero when
// none.
func (s *Service) Next(ctx context.Context) (time.Time, error) {
	ws, err := s.d.Store.Due(ctx, farFuture)
	if err != nil {
		return time.Time{}, err
	}
	now := s.d.Clock.Now()
	var out time.Time
	for _, w := range ws {
		for _, at := range []time.Time{w.Until, w.Expires} {
			if at.After(now) && (out.IsZero() || at.Before(out)) {
				out = at
			}
		}
	}
	return out, nil
}

var farFuture = time.Date(9999, 1, 1, 0, 0, 0, 0, time.UTC)
