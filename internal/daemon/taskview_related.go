package daemon

import (
	"context"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/domain/task"
)

// relatedOf sets the tasks e's property of role related lists, each read through its aliases: one that
// reads as another task is Merged.
func (ev *taskViewer) relatedOf(ctx context.Context, e task.Task, v *api.TaskView) error {
	return nil
}
