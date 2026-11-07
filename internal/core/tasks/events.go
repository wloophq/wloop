package tasks

import (
	"context"
	"slices"
	"time"

	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/domain/link"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
)

// createdEvents are the task.EventCreated events of the tasks cs create.
func createdEvents(cs []task.Change, now time.Time) []port.Event {
	var out []port.Event
	for _, c := range cs {
		if c.Op == task.OpCreate {
			out = append(out, port.Event{Unit: task.UnitOf(c.Task.ID), Kind: task.EventCreated, At: now})
		}
	}
	return out
}

// Linked records task.EventLinked on the own unit of each task holding, open or resolved, the item a link of ls
// names, once per link; its data is the unit the link targets. A link that names no item records nothing.
func (s *Service) Linked(ctx context.Context, ls []link.Link) error {
	now := s.now()
	have := map[unit.Unit][]task.Task{}
	var evs []port.Event
	for _, l := range ls {
		if l.Item == "" {
			continue
		}
		es, ok := have[l.From]
		if !ok {
			var err error
			if es, err = s.d.Store.TasksOf(ctx, l.From); err != nil {
				return err
			}
			have[l.From] = es
		}
		ref := task.ItemRef{Unit: l.From, Item: l.Item}
		for _, e := range es {
			if slices.Contains(e.Items, ref) || slices.Contains(e.Resolved, ref) {
				evs = append(evs, port.Event{Unit: task.UnitOf(e.ID), Kind: task.EventLinked, Key: linkKey(l), Data: l.To.String(), At: now})
			}
		}
	}
	if len(evs) == 0 {
		return nil
	}
	return s.d.Store.Apply(ctx, nil, evs...)
}

func linkKey(l link.Link) string {
	return l.From.String() + "#" + l.Item + ">" + string(l.Kind) + ":" + l.To.String()
}
