package facttask

import (
	"context"

	"github.com/wloophq/wloop/internal/domain/task"
)

// LinkTargetDeps are what task.link_target_tasks reads.
type LinkTargetDeps struct{}

// linkTargetTasks is task.link_target_tasks of e, without refused.
func (p *Provider) linkTargetTasks(ctx context.Context, e task.Task, refused []string) ([]string, error) {
	return []string{}, nil
}
