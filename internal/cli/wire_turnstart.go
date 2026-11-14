package cli

import (
	"context"
	"fmt"

	"github.com/wloophq/wloop/internal/domain/unit"
	src "github.com/wloophq/wloop/pkg/plugin/source"
)

// turnStartDeps are what turn.start reads on units outside the round
// providers: the headless runner and the count of automated turns.
type turnStartDeps struct {
	Headless headlessStarter
	Turns    automatedTurns
}

// wireTurnStart wires turn.start on units other than pull requests and its daily budget.
func (e *Env) wireTurnStart(w *serveWiring) {
	if w.Deps != nil && w.Deps.Headless != nil {
		w.Engine.TurnStart.Headless = w.Deps.Headless
	}
	if w.DB != nil {
		w.Engine.TurnStart.Turns = w.DB
	}
}

// servedPreparer runs a turn's prepare in the daemon serve built.
type servedPreparer struct{ e *Env }

func (p servedPreparer) PrepareTurn(ctx context.Context, intent string, u unit.Unit, guidance string, inputs map[string]any) (map[string]any, error) {
	if p.e.linkStates != nil {
		if s := p.e.linkStates.Load(); s != nil {
			return s.PrepareTurn(ctx, intent, u, guidance, inputs)
		}
	}
	return nil, fmt.Errorf("%w: no daemon runs the prepare of %s yet", src.ErrUnavailable, intent)
}
