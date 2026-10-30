package tasks

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
)

// Nudged records a nudge sent at at on u about the items its draft listed: it writes the last_nudge property
// on the task id names and on every open task that holds one of the items on u, as the workflow that nudged.
// It reads them under the service's lock, so it never writes back a copy older than a concurrent Sync's, and
// it never moves last_nudge back.
func (s *Service) Nudged(ctx context.Context, id task.ID, u unit.Unit, its []string, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	var es []task.Task
	if len(its) > 0 {
		got, err := s.d.Store.TasksOf(ctx, u)
		if err != nil {
			return err
		}
		for _, e := range got {
			if slices.ContainsFunc(e.Items, func(r task.ItemRef) bool { return r.Unit == u && slices.Contains(its, r.Item) }) {
				es = append(es, e)
			}
		}
	}
	if id != "" && !slices.ContainsFunc(es, func(e task.Task) bool { return e.ID == id }) {
		e, err := s.d.Store.Task(ctx, id)
		switch {
		case err == nil:
			es = append(es, e)
		case !errors.Is(err, ErrNoTask):
			return err
		}
	}
	now := s.now()
	var cs []task.Change
	for _, e := range es {
		if !e.Open() || !at.After(e.Prop(task.PropLastNudge).Time) {
			continue
		}
		e.SetProp(task.PropLastNudge, task.Value{Time: at}, task.WriterWorkflow, now)
		e.Updated = now
		cs = append(cs, task.Change{Op: task.OpUpdate, Task: e, Why: "nudged"})
	}
	if len(cs) == 0 {
		return nil
	}
	return s.d.Store.Apply(ctx, cs)
}
