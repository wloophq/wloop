package cli

import (
	"context"
	"errors"
	"slices"

	"github.com/wloophq/wloop/internal/core/gates"
	coretasks "github.com/wloophq/wloop/internal/core/tasks"
	"github.com/wloophq/wloop/internal/domain/task"
	intentload "github.com/wloophq/wloop/internal/intent"
)

// verbsHandledBy are the profile's verbs whose handler is handler, as it loads now, sorted.
func verbsHandledBy(loaded func() *intentload.Loaded, handler string) func() []string {
	return func() []string {
		var out []string
		for name, v := range loaded().Tasks.Verbs {
			if v.Handler == handler {
				out = append(out, name)
			}
		}
		slices.Sort(out)
		return out
	}
}

// mootMerges supersedes, as moot, the open gates proposing a merge or a relation of task id, which ended: both
// pair two open tasks.
func (e *Env) mootMerges(ctx context.Context, g *gates.Service, id task.ID) {
	acting, err := g.ActingFor(ctx, id)
	if err != nil {
		e.warnf("merge and relate proposals of ended task %s: %v", id, err)
		return
	}
	for _, o := range acting {
		p, ok := coretasks.ProposedVerb(o)
		if h := e.loadedIntents().Tasks.Handler(string(p.Verb)); !ok || h != coretasks.HandlerMerge && h != coretasks.HandlerRelate {
			continue
		}
		if err := g.Supersede(ctx, o.ID, task.ActorWorkflow); err != nil && !errors.Is(err, gates.ErrResolved) {
			e.warnf("proposal %d of ended task %s: %v", o.ID, id, err)
		}
	}
}
