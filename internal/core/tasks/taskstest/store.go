// Package taskstest holds an in-memory task store and the contract suite every task store passes.
package taskstest

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"sync"

	"github.com/wloophq/wloop/internal/core/port"
	coretasks "github.com/wloophq/wloop/internal/core/tasks"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
)

// Store is an in-memory coretasks.Store. Notes counts the Applies that changed something, Applies every call.
type Store struct {
	mu      sync.Mutex
	epoch   string
	rev     uint64
	tasks   map[task.ID]task.Task
	revs    map[task.ID]uint64
	Notes   int
	Applies int
	Events  []port.Event
}

var _ coretasks.Store = (*Store)(nil)

func New() *Store {
	return &Store{epoch: "fake", tasks: map[task.ID]task.Task{}, revs: map[task.ID]uint64{}}
}

func (s *Store) all() []task.Task {
	out := make([]task.Task, 0, len(s.tasks))
	for _, e := range s.tasks {
		out = append(out, e)
	}
	slices.SortFunc(out, func(a, b task.Task) int { return cmp.Or(a.Created.Compare(b.Created), cmp.Compare(a.ID, b.ID)) })
	return out
}

// selected are the tasks of all q selects: every one, or the open ones.
func selected(all []task.Task, q task.Query) []task.Task {
	if q.All {
		return all
	}
	return slices.DeleteFunc(all, func(e task.Task) bool { return !e.Open() })
}

func (s *Store) Tasks(ctx context.Context, q task.Query) ([]task.Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return selected(s.all(), q), nil
}

func (s *Store) TasksSince(ctx context.Context, q task.Query, since coretasks.Since) (coretasks.Page, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	all := s.all()
	p := coretasks.Page{Epoch: s.epoch, Rev: s.rev}
	if since.Epoch != s.epoch || since.Rev > s.rev {
		p.Full, p.Tasks = true, selected(all, q)
		return p, nil
	}
	p.Tasks = slices.DeleteFunc(all, func(e task.Task) bool { return s.revs[e.ID] <= since.Rev })
	return p, nil
}

func (s *Store) Task(ctx context.Context, id task.ID) (task.Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.tasks[id]
	if !ok {
		return task.Task{}, fmt.Errorf("%w: %s", coretasks.ErrNoTask, id)
	}
	return e, nil
}

func (s *Store) TasksOf(ctx context.Context, u unit.Unit) ([]task.Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []task.Task
	for _, e := range s.all() {
		if slices.Contains(e.Units, u) || slices.ContainsFunc(e.Items, func(r task.ItemRef) bool { return r.Unit == u }) {
			out = append(out, e)
		}
	}
	return out, nil
}

func (s *Store) Apply(ctx context.Context, cs []task.Change, evs ...port.Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Applies++
	var changed []task.Task
	for _, c := range cs {
		if c.Task.ID == "" {
			return fmt.Errorf("a task without an id")
		}
		if old, ok := s.tasks[c.Task.ID]; ok && old.Same(c.Task) {
			continue
		}
		changed = append(changed, c.Task)
	}
	if len(changed) == 0 {
		if len(cs) == 0 {
			s.Events = append(s.Events, evs...)
		}
		return nil
	}
	s.Events = append(s.Events, evs...)
	s.rev++
	for _, e := range changed {
		s.tasks[e.ID], s.revs[e.ID] = e, s.rev
	}
	s.Notes++
	return nil
}
