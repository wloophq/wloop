package cli

import (
	"context"

	coretasks "github.com/wloophq/wloop/internal/core/tasks"
	"github.com/wloophq/wloop/internal/domain/gate"
	"github.com/wloophq/wloop/internal/domain/merge"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/workflow"
	intentload "github.com/wloophq/wloop/internal/intent"
	"github.com/wloophq/wloop/internal/intent/builtin"
)

// survives is the survivor predicate of tasks.toml's verb over task t's facts, as the profile loads now; a
// verb that names none holds on no task.
func survives(loaded func() *intentload.Loaded, facts func(ctx context.Context, t task.Task) (workflow.Facts, error)) coretasks.Survives {
	return func(ctx context.Context, verb string, t task.Task) (bool, error) {
		pred := loaded().Tasks.Verbs[verb].Survivor
		if pred == nil {
			return false, nil
		}
		f, err := facts(ctx, t)
		if err != nil {
			return false, err
		}
		return pred.Match(workflow.Vars{Facts: f})
	}
}

// verbGateText words the gate verb opens on e with args as gates.toml's text over the proposal facts: the
// verb, both tasks' titles, and, the survivor read now, the one a merge keeps and the one it folds.
func verbGateText(loaded func() *intentload.Loaded, tasks interface {
	Task(ctx context.Context, id task.ID) (task.Task, error)
}, holds coretasks.Survives) coretasks.GateText {
	return func(ctx context.Context, g gate.Gate, verb string, e task.Task, args map[string]string) (string, error) {
		def, ok := loaded().Gates.Gate(g.Name)
		if !ok {
			return "", nil
		}
		slot := string(builtin.ProposalSlot) + "."
		facts := map[string]any{slot + "verb": verb, slot + "task": e.Title}
		if id := args[task.ArgTask]; id != "" {
			o, err := tasks.Task(ctx, task.ID(id))
			if err != nil {
				return "", err
			}
			facts[slot+"other"] = o.Title
			if loaded().Tasks.Handler(verb) == coretasks.HandlerMerge {
				eHolds, err := holds(ctx, verb, e)
				if err != nil {
					return "", err
				}
				oHolds, err := holds(ctx, verb, o)
				if err != nil {
					return "", err
				}
				keep, fold := merge.Survivor(e, o, eHolds, oHolds)
				facts[slot+"keep"], facts[slot+"fold"] = keep.Title, fold.Title
			}
		}
		return def.Words(intentload.ProposalFacts(func(name string) any { return facts[name] }))
	}
}

// shapingVerbs are the profile's verbs that shape a task by hand, as it loads now.
func shapingVerbs(loaded func() *intentload.Loaded) func() []string {
	return func() []string { return loaded().Tasks.ShapingVerbs() }
}
