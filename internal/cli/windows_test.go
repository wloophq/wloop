package cli

import (
	"maps"
	"slices"

	"github.com/wloophq/wloop/internal/config"
	"github.com/wloophq/wloop/internal/domain/workflow"
	"github.com/wloophq/wloop/internal/intent/builtin"
	"github.com/wloophq/wloop/internal/turn"
)

// withoutWindows is cfg with every bundled turn.start workflow sending its
// round at once, as a user who sets their approval.window to 0.
func withoutWindows(cfg config.Config) config.Config {
	l, err := turn.Bundled()
	if err != nil {
		panic(err)
	}
	cfg.Workflows = maps.Clone(cfg.Workflows)
	if cfg.Workflows == nil {
		cfg.Workflows = map[string]config.WorkflowConfig{}
	}
	zero := config.Days(0)
	for _, w := range l.Workflows {
		if !slices.ContainsFunc(w.Steps, func(st workflow.Step) bool { return st.Use == builtin.StepTurnStart }) {
			continue
		}
		wc := cfg.Workflows[w.Name]
		a := config.ApprovalConfig{}
		if wc.Approval != nil {
			a = *wc.Approval
		}
		a.Window = &zero
		wc.Approval = &a
		cfg.Workflows[w.Name] = wc
	}
	return cfg
}
