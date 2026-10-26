package automation

import (
	"context"

	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/workflow"
)

// Scoper is an optional capability of Units: the facts of u that depend on
// the workflow reading them, such as the settlement of the drafts it
// proposed, laid over the unit's facts f. It returns a new map and never
// writes f, which the other workflows read.
type Scoper interface {
	Scope(ctx context.Context, workflow string, u unit.Unit, f workflow.Facts) (workflow.Facts, error)
}

// factsFor are u's facts as workflow w reads them.
func (e *Engine) factsFor(ctx context.Context, w string, u unit.Unit) (workflow.Facts, error) {
	f, err := e.Units.Facts(ctx, u)
	if err != nil {
		return nil, err
	}
	return e.scoped(ctx, w, u, f)
}

func (e *Engine) scoped(ctx context.Context, w string, u unit.Unit, f workflow.Facts) (workflow.Facts, error) {
	s, ok := e.Units.(Scoper)
	if !ok {
		return f, nil
	}
	return s.Scope(ctx, w, u, f)
}
