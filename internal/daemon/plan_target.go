package daemon

import (
	"context"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/workflow"
	"github.com/wloophq/wloop/internal/intent/builtin"
	"github.com/wloophq/wloop/internal/readmodel"
)

// itemsLoader is an items ledger that reads the items of many units at once.
type itemsLoader interface {
	Load(ctx context.Context, units []unit.Unit) (map[unit.Unit][]items.Item, error)
}

// planViews are its as the API serves them, without their actions, each unit presented as known presents it: a
// task's item names its task, by title, with the units the task came from, and says which open items an
// items.resolve resolves. The tasks come from one pass and their units' items from one read.
func (s *Server) planViews(ctx context.Context, its []workflow.Item, known map[unit.Unit]readmodel.Presentation) []api.PlanItem {
	out := make([]api.PlanItem, len(its))
	for i, it := range its {
		out[i] = s.planItemView(it, known[it.Unit])
	}
	tasks := s.planTasks(ctx, its)
	if len(tasks) == 0 {
		return out
	}
	var units []unit.Unit
	for _, e := range tasks {
		units = append(units, e.Units...)
	}
	shown := s.presentations(units)
	held := s.openItemsOf(ctx, tasks)
	for i, it := range its {
		id, _ := task.IDOf(it.Unit)
		e, ok := tasks[id]
		if !ok {
			continue
		}
		tg := &api.PlanTarget{Task: string(e.ID), Title: e.Title}
		for _, u := range e.Units {
			tg.Units = append(tg.Units, api.PlanUnit{Label: shown[u].Label, Link: shown[u].Link})
		}
		out[i].Target = tg
		for j := range out[i].Does {
			if out[i].Does[j].Use == builtin.StepItemsResolve {
				out[i].Does[j].Items = held[e.ID]
			}
		}
	}
	return out
}

// planTasks are the stored tasks the items of its act on.
func (s *Server) planTasks(ctx context.Context, its []workflow.Item) map[task.ID]task.Task {
	if s.deps.Tasks == nil {
		return nil
	}
	var ids []task.ID
	for _, it := range its {
		if id, ok := task.IDOf(it.Unit); ok {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	tasks, err := s.deps.Tasks.Stored(ctx, ids)
	if err != nil {
		s.logf("plan: the tasks of its items: %v", err)
	}
	return tasks
}

// openItemsOf are the summaries of the open items each task of tasks holds, by task, in its order, from one read
// of their units' items when the ledger reads many units at once.
func (s *Server) openItemsOf(ctx context.Context, tasks map[task.ID]task.Task) map[task.ID][]string {
	if s.deps.Items == nil {
		return nil
	}
	var units []unit.Unit
	seen := map[unit.Unit]bool{}
	for _, e := range tasks {
		for _, r := range e.Items {
			if !seen[r.Unit] {
				seen[r.Unit] = true
				units = append(units, r.Unit)
			}
		}
	}
	byUnit, err := s.itemsOfUnits(ctx, units)
	if err != nil {
		s.logf("plan: the items of its tasks: %v", err)
	}
	out := map[task.ID][]string{}
	for id, e := range tasks {
		for _, r := range e.Items {
			for _, it := range byUnit[r.Unit] {
				if it.ID == r.Item && it.State == items.Open {
					out[id] = append(out[id], it.Summary)
				}
			}
		}
	}
	return out
}

func (s *Server) itemsOfUnits(ctx context.Context, units []unit.Unit) (map[unit.Unit][]items.Item, error) {
	if len(units) == 0 {
		return nil, nil
	}
	if l, ok := s.deps.Items.(itemsLoader); ok {
		return l.Load(ctx, units)
	}
	out := make(map[unit.Unit][]items.Item, len(units))
	for _, u := range units {
		its, err := s.deps.Items.Items(ctx, u)
		if err != nil {
			return out, err
		}
		out[u] = its
	}
	return out, nil
}
