package facttask

import (
	"context"
	"slices"

	coretasks "github.com/wloophq/wloop/internal/core/tasks"
	"github.com/wloophq/wloop/internal/domain/task"
)

// ClarityDeps are what task.clarified reads.
type ClarityDeps struct {
	// Shaping are the verbs that shape a task by hand: those whose move has a state to go to, and those that
	// file it under a project; nil: none.
	Shaping func() []string
	// ByHand are, of ids, or of every task when ids is empty, those on whose own unit a person, or an agent
	// through a session the user drives, ran a verb whose event is one of kinds; nil: none.
	ByHand func(ctx context.Context, kinds []string, ids ...task.ID) (map[task.ID]bool, error)
}

// clarifiedKey is the pass's Once key of the tasks a person shaped with a verb.
const clarifiedKey = "clarified"

// clarified is task.clarified of e, as builtin.Facts says.
func (p *Provider) clarified(ctx context.Context, e task.Task) (bool, error) {
	var loop []string
	if p.d.RoleProp != nil {
		for _, r := range task.LoopRoles {
			if name := p.d.RoleProp(r); name != "" {
				loop = append(loop, name)
			}
		}
	}
	for name, w := range e.Written {
		if (w.By == task.WriterPerson || w.By == task.WriterAgent) && !slices.Contains(loop, name) && !e.Props[name].IsZero() {
			return true, nil
		}
	}
	if p.d.Clarity.Shaping == nil || p.d.Clarity.ByHand == nil {
		return false, nil
	}
	kinds := task.VerbEventKinds(p.d.Clarity.Shaping())
	if len(kinds) == 0 {
		return false, nil
	}
	if _, ok := coretasks.PassOf(ctx); ok {
		all, err := coretasks.Once(ctx, clarifiedKey, func() (map[task.ID]bool, error) { return p.d.Clarity.ByHand(ctx, kinds) })
		return all[e.ID], err
	}
	one, err := p.d.Clarity.ByHand(ctx, kinds, e.ID)
	return one[e.ID], err
}
