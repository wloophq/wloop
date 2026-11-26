package daemon

import (
	"context"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/domain/merge"
	"github.com/wloophq/wloop/internal/domain/task"
)

// mergedFromOf sets the tasks merged into e, the top level of its property of role merged, each as its
// merge record keeps it, with the tasks merged into each in Within.
func (ev *taskViewer) mergedFromOf(ctx context.Context, e task.Task, v *api.TaskView) error {
	name, _ := ev.s.loaded().Tasks.PropOf(task.RoleMerged)
	if name == "" {
		return nil
	}
	ids := e.Prop(name).List
	records := make(map[string]merge.Record, len(ids))
	for _, id := range ids {
		r, ok, err := ev.g.MergeOf(ctx, task.ID(id))
		if err != nil {
			return err
		}
		if ok {
			records[id] = r
		}
	}
	v.MergedFrom = mergedInto(e.ID, ids, records)
	return nil
}

// mergedInto are the tasks of ids whose merge record names into, in ids' order, each with those merged into
// it in Within.
func mergedInto(into task.ID, ids []string, records map[string]merge.Record) []api.TaskRef {
	var out []api.TaskRef
	for _, id := range ids {
		r, ok := records[id]
		if !ok || r.Into != into {
			continue
		}
		out = append(out, api.TaskRef{ID: id, Title: r.Task.Title, State: string(r.Task.State), Within: mergedInto(r.From, ids, records)})
	}
	return out
}
