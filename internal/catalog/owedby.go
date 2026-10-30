package catalog

import (
	"context"
	"fmt"
	"time"

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

// Row is what a task's row shows of its facts: who owes its next step (task.owed_by), who waits on you through
// it (task.askers) and how long its links' messages have waited to be extracted (task.extraction_lag).
type Row struct {
	OwedBy        string
	Askers        []string
	ExtractionLag time.Duration
}

// RowOf is the Row of a task over the facts facts reads of it, the task facts provider's.
func RowOf(facts func(ctx context.Context, t task.Task) (workflow.Facts, error)) func(ctx context.Context, t task.Task) (Row, error) {
	return func(ctx context.Context, t task.Task) (Row, error) {
		f, err := facts(ctx, t)
		if err != nil {
			return Row{}, err
		}
		owed, _ := f["task.owed_by"].(string)
		askers, _ := f["task.askers"].([]string)
		lag, _ := f["task.extraction_lag"].(time.Duration)
		return Row{OwedBy: owed, Askers: askers, ExtractionLag: lag}, nil
	}
}
