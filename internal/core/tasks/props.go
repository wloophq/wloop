package tasks

import (
	"context"
	"fmt"

	"github.com/wloophq/wloop/internal/domain/task"
)

var _ PropStore = (*Service)(nil)

// SetProp sets task id's property name to v as w wrote it, under the lock every other write of the service
// takes, so no write built from an older copy of the task undoes it; a zero v unsets it. It checks no type and
// no writer: the caller holds the profile that declares them.
func (s *Service) SetProp(ctx context.Context, id task.ID, name string, v task.Value, w task.Writer) error {
	if name == "" {
		return fmt.Errorf("%w: a property needs a name", ErrArgs)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	e, err := s.d.Store.Task(ctx, id)
	if err != nil {
		return err
	}
	next := e
	next.SetProp(name, v, w, s.now())
	if next.Same(e) {
		return nil
	}
	next.Updated = s.now()
	return s.d.Store.Apply(ctx, []task.Change{{Op: task.OpUpdate, Task: next, Why: "set " + name}})
}

// PropsOf are task id's properties, empty for one with none.
func (s *Service) PropsOf(ctx context.Context, id task.ID) (task.Props, error) {
	e, err := s.d.Store.Task(ctx, id)
	if err != nil {
		return nil, err
	}
	out := task.Props{}
	for name, v := range e.Props {
		out[name] = v
	}
	return out, nil
}
