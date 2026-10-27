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

// EventFacts is an optional capability of Units: the facts of a unit an
// event names, served after the unit left its list (an entry done or
// dismissed), so an event workflow reacts to what took it out.
type EventFacts interface {
	EventFacts(ctx context.Context, u unit.Unit) (workflow.Facts, error)
}

// factsFor are u's facts as workflow w reads them.
func (e *Engine) factsFor(ctx context.Context, w workflow.Workflow, u unit.Unit) (workflow.Facts, error) {
	f, err := e.unitFacts(ctx, w, u)
	if err != nil {
		return nil, err
	}
	return e.scoped(ctx, w.Name, u, f)
}

// unitFacts are u's facts, through EventFacts for an event workflow.
func (e *Engine) unitFacts(ctx context.Context, w workflow.Workflow, u unit.Unit) (workflow.Facts, error) {
	if ef, ok := e.Units.(EventFacts); ok && w.Trigger.Kind == workflow.TriggerEvent {
		return ef.EventFacts(ctx, u)
	}
	return e.Units.Facts(ctx, u)
}

func (e *Engine) scoped(ctx context.Context, w string, u unit.Unit, f workflow.Facts) (workflow.Facts, error) {
	s, ok := e.Units.(Scoper)
	if !ok {
		return f, nil
	}
	return s.Scope(ctx, w, u, f)
}
