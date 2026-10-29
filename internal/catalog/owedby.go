package catalog

import (
	"context"
	"fmt"

	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/workflow"
	intentload "github.com/wloophq/wloop/internal/intent"
)

// OwedByOf is who owes the next step of the task whose facts f holds, as
// tasks.toml's owed_by.expr says over them: me, a person's key, or "" when
// nobody does, and when the profile names no expression. loaded is the
// profile as it loads now.
func OwedByOf(loaded func() *intentload.Loaded) func(ctx context.Context, f workflow.Facts) (string, error) {
	return func(_ context.Context, f workflow.Facts) (string, error) {
		prog := loaded().Tasks.Owed
		if prog == nil {
			return "", nil
		}
		who, err := prog.Text(f)
		if err != nil {
			return "", fmt.Errorf("tasks.toml owed_by.expr: %w", err)
		}
		return who, nil
	}
}

// OwedBy is the core's tasks.OwedBy: OwedByOf over the facts facts reads of
// a task, the task facts provider's.
func OwedBy(loaded func() *intentload.Loaded, facts func(ctx context.Context, t task.Task) (workflow.Facts, error)) func(ctx context.Context, t task.Task) (string, error) {
	of := OwedByOf(loaded)
	return func(ctx context.Context, t task.Task) (string, error) {
		f, err := facts(ctx, t)
		if err != nil {
			return "", err
		}
		return of(ctx, f)
	}
}
