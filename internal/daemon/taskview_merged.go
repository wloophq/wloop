package daemon

import (
	"context"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/domain/task"
)

// mergedFromOf sets the tasks merged into e, the top level of its property of role merged, each as its
// merge record keeps it, with the tasks merged into each in Within.
func (ev *taskViewer) mergedFromOf(ctx context.Context, e task.Task, v *api.TaskView) error {
	name, _ := ev.s.loaded().Tasks.PropOf(task.RoleMerged)
	if name == "" {
		return nil
	}
	refs, err := ev.mergedInto(ctx, e.ID, e.Prop(name).List)
	v.MergedFrom = refs
	return err
}

// mergedInto are the tasks of ids whose merge record names into, in ids' order, each with those merged into
// it in Within.
func (ev *taskViewer) mergedInto(ctx context.Context, into task.ID, ids []string) ([]api.TaskRef, error) {
	var out []api.TaskRef
	for _, id := range ids {
		r, ok, err := ev.g.MergeOf(ctx, task.ID(id))
		if err != nil {
			return nil, err
		}
		if !ok || r.Into != into {
			continue
		}
		ref := api.TaskRef{ID: id, Title: r.Task.Title, State: string(r.Task.State)}
		if ref.Within, err = ev.mergedInto(ctx, r.From, ids); err != nil {
			return nil, err
		}
		out = append(out, ref)
	}
	return out, nil
}
