package facttask

import (
	"context"

	"github.com/wloophq/wloop/internal/domain/task"
)

// CandidateDeps are what task.candidates reads.
type CandidateDeps struct{}

// candidates is task.candidates of e, without refused.
func (p *Provider) candidates(ctx context.Context, e task.Task, refused []string) ([]string, error) {
	return []string{}, nil
}
