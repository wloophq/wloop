package facttask

import (
	"context"

	"github.com/wloophq/wloop/internal/domain/task"
)

// RefusalDeps are what task.refused_merges reads.
type RefusalDeps struct{}

// refusedMerges is task.refused_merges of e.
func (p *Provider) refusedMerges(ctx context.Context, e task.Task) ([]string, error) {
	return []string{}, nil
}
