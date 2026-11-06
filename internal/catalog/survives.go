package catalog

import (
	"context"

	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/workflow"
	intentload "github.com/wloophq/wloop/internal/intent"
)

// Survives is the core's tasks.Survives: the survivor predicate of tasks.toml's verb over task t's facts, as
// the profile loads now; a verb that names none holds on no task.
func Survives(loaded func() *intentload.Loaded, facts func(ctx context.Context, t task.Task) (workflow.Facts, error)) func(ctx context.Context, verb string, t task.Task) (bool, error) {
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
