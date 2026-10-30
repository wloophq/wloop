package source

import (
	"context"
	"fmt"
	"slices"

	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/wake"
	"github.com/wloophq/wloop/pkg/plugin"
	src "github.com/wloophq/wloop/pkg/plugin/source"
)

// TaskWakes are the host's tasks as their wakes move them: TasksOf are the
// tasks that hold a unit, Sleep defers a task under a wake, and Rouse drops
// its wake and returns it where the wake said.
type TaskWakes interface {
	TasksOf(ctx context.Context, u unit.Unit) ([]task.Task, error)
	Sleep(ctx context.Context, id task.ID, w wake.Wake) error
	Rouse(ctx context.Context, id task.ID) error
}

type wakes struct{ b *Broker }

// SetWake puts w on the task w names, or on every open task of w's unit when
// it names none; a unit no open task holds has no task to wake. A When that
// is a list fact alone, such as task.link_owes, wakes each task once its own
// value of the fact changes from the one it has now.
func (k wakes) SetWake(ctx context.Context, w src.Wake) error {
	ts, err := k.tasks(ctx, w.Unit, w.Task)
	if err != nil {
		return err
	}
	if len(ts) == 0 {
		return fmt.Errorf("%w: no open task holds %s", src.ErrNotFound, w.Unit.Key)
	}
	for _, t := range ts {
		if err := k.b.Wakes.Sleep(ctx, t.ID, wake.Wake{Task: t.ID, Until: w.Until, When: w.When}); err != nil {
			return err
		}
	}
	return nil
}

// ClearWake drops the wake of the task named, or of every open task of u.
func (k wakes) ClearWake(ctx context.Context, u plugin.Unit, id string) error {
	ts, err := k.tasks(ctx, u, id)
	if err != nil {
		return err
	}
	for _, t := range ts {
		if err := k.b.Wakes.Rouse(ctx, t.ID); err != nil {
			return err
		}
	}
	return nil
}

// tasks are the open tasks of the plugin's own unit w, only id's when id is
// set.
func (k wakes) tasks(ctx context.Context, w plugin.Unit, id string) ([]task.Task, error) {
	if k.b.Wakes == nil {
		return nil, src.ErrUnsupported
	}
	u, err := k.b.Units.Own(k.b.ID, w)
	if err != nil {
		return nil, err
	}
	ts, err := k.b.Wakes.TasksOf(ctx, u)
	if err != nil {
		return nil, err
	}
	ts = slices.DeleteFunc(ts, func(t task.Task) bool { return !t.Open() || id != "" && string(t.ID) != id })
	if id != "" && len(ts) == 0 {
		return nil, fmt.Errorf("%w: task %s does not hold %s", src.ErrNotFound, id, w.Key)
	}
	return ts, nil
}
