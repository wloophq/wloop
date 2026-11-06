package daemon

import (
	"context"
	"errors"

	"github.com/wloophq/wloop/internal/api"
	coretasks "github.com/wloophq/wloop/internal/core/tasks"
	"github.com/wloophq/wloop/internal/domain/task"
)

type taskRef struct {
	t  task.Task
	ok bool
}

// knowTasks keeps es as the tasks their ids read as, so a related id on the page reads no store.
func (ev *taskViewer) knowTasks(es []task.Task) {
	if ev.refs == nil {
		ev.refs = map[task.ID]taskRef{}
	}
	for _, e := range es {
		ev.refs[e.ID] = taskRef{t: e, ok: true}
	}
}

// taskOf is the task id reads as, aliases followed, read once per viewer; false for none.
func (ev *taskViewer) taskOf(ctx context.Context, id task.ID) (task.Task, bool, error) {
	if r, seen := ev.refs[id]; seen {
		return r.t, r.ok, nil
	}
	o, err := ev.g.Task(ctx, id)
	if err != nil && !errors.Is(err, coretasks.ErrNoTask) {
		return task.Task{}, false, err
	}
	if ev.refs == nil {
		ev.refs = map[task.ID]taskRef{}
	}
	ev.refs[id] = taskRef{t: o, ok: err == nil}
	return o, err == nil, nil
}

// relatedOf sets the tasks e's property of role related lists, each read through its aliases: one that
// reads as another task is Merged. An entry that reads as no task is left out, and one that reads as e or as
// a task listed before it too.
func (ev *taskViewer) relatedOf(ctx context.Context, e task.Task, v *api.TaskView) error {
	name, _ := ev.s.loaded().Tasks.PropOf(task.RoleRelated)
	if name == "" {
		return nil
	}
	seen := map[task.ID]bool{e.ID: true}
	for _, id := range e.Prop(name).List {
		o, ok, err := ev.taskOf(ctx, task.ID(id))
		if err != nil {
			return err
		}
		if !ok || seen[o.ID] {
			continue
		}
		seen[o.ID] = true
		v.Related = append(v.Related, api.TaskRef{ID: id, Title: o.Title, State: string(o.State), Merged: o.ID != task.ID(id)})
	}
	return nil
}
