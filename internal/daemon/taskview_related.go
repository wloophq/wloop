package daemon

import (
	"context"
	"errors"

	"github.com/wloophq/wloop/internal/api"
	coretasks "github.com/wloophq/wloop/internal/core/tasks"
	"github.com/wloophq/wloop/internal/domain/task"
)

// relatedOf sets the tasks e's property of role related lists, each read through its aliases: one that
// reads as another task is Merged. An entry that reads as no task is left out, and one that reads as a task
// listed before it too.
func (ev *taskViewer) relatedOf(ctx context.Context, e task.Task, v *api.TaskView) error {
	name, _ := ev.s.loaded().Tasks.PropOf(task.RoleRelated)
	if name == "" {
		return nil
	}
	seen := map[task.ID]bool{}
	for _, id := range e.Prop(name).List {
		o, err := ev.g.Task(ctx, task.ID(id))
		if errors.Is(err, coretasks.ErrNoTask) {
			continue
		}
		if err != nil {
			return err
		}
		if seen[o.ID] || o.ID == e.ID {
			continue
		}
		seen[o.ID] = true
		v.Related = append(v.Related, api.TaskRef{ID: id, Title: o.Title, State: string(o.State), Merged: o.ID != task.ID(id)})
	}
	return nil
}
