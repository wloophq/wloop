package cli

import (
	"context"

	coretasks "github.com/wloophq/wloop/internal/core/tasks"
	"github.com/wloophq/wloop/internal/domain/gate"
	"github.com/wloophq/wloop/internal/domain/merge"
	"github.com/wloophq/wloop/internal/domain/task"
	intentload "github.com/wloophq/wloop/internal/intent"
	"github.com/wloophq/wloop/internal/intent/builtin"
)

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
