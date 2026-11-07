package automation

import (
	"context"

	"github.com/wloophq/wloop/internal/domain/unit"
)

// Skipper is a capability of the engine's Keep: the workflows the user stopped on a subject, which the
// engine plans nothing of there.
type Skipper interface {
	Skipped(ctx context.Context, workflow string, u unit.Unit) (bool, error)
}

// skipped says whether the Keep skips workflow on u; an error is logged and reads as not skipped.
func (p *planner) skipped(workflow string, u unit.Unit) bool {
	s, ok := p.e.Keep.(Skipper)
	if !ok {
		return false
	}
	skip, err := s.Skipped(p.ctx, workflow, u)
	if err != nil {
		p.errorf("%s: %s: reading its skips: %v", workflow, u, err)
	}
	return skip
}
