package cli

import (
	"context"

	coretasks "github.com/wloophq/wloop/internal/core/tasks"
	"github.com/wloophq/wloop/internal/domain/gate"
	intentload "github.com/wloophq/wloop/internal/intent"
	"github.com/wloophq/wloop/internal/intent/builtin"
)

// verbGateText words a gated verb's gate as gates.toml's text over the proposal facts: the verb, both tasks'
// titles, and the one a merge keeps and the one it folds.
func verbGateText(loaded func() *intentload.Loaded) coretasks.GateText {
	return func(_ context.Context, g gate.Gate, w coretasks.GateWords) (string, error) {
		def, ok := loaded().Gates.Gate(g.Name)
		if !ok {
			return "", nil
		}
		slot := string(builtin.ProposalSlot) + "."
		facts := map[string]any{slot + "verb": w.Verb, slot + "task": w.Task.Title}
		if w.Other.ID != "" {
			facts[slot+"other"] = w.Other.Title
		}
		if w.Keep.ID != "" {
			facts[slot+"keep"], facts[slot+"fold"] = w.Keep.Title, w.Fold.Title
		}
		return def.Words(intentload.ProposalFacts(func(name string) any { return facts[name] }))
	}
}

// shapingVerbs are the profile's verbs that shape a task by hand, as it loads now.
func shapingVerbs(loaded func() *intentload.Loaded) func() []string {
	return func() []string { return loaded().Tasks.ShapingVerbs() }
}
