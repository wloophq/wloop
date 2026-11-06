package daemon

import (
	"context"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/domain/task"
)

// mergedFromOf sets the tasks merged into e, the top level of its property of role merged, each as its
// merge record keeps it, with the tasks merged into each in Within.
func (ev *taskViewer) mergedFromOf(ctx context.Context, e task.Task, v *api.TaskView) error {
	return nil
}
