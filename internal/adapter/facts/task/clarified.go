package facttask

import (
	"context"

	"github.com/wloophq/wloop/internal/domain/task"
)

// ClarityDeps are what task.clarified reads.
type ClarityDeps struct{}

// clarified is task.clarified of e, as builtin.Facts says.
func (p *Provider) clarified(ctx context.Context, e task.Task) (bool, error) {
	return false, nil
}
