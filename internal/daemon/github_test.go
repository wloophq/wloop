package daemon

import (
	"context"

	"github.com/wloophq/wloop/internal/core/turns"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/readmodel/prrow"
)

// turnFacts reads every book of s once and resolves a started turn against
// the agents' last known states, as the GitHub rows do.
func turnFacts(s *Server, ctx context.Context, reg *dintent.Registry) (map[pr.Ref]prrow.TurnFact, error) {
	if s.deps.Turns == nil {
		return nil, nil
	}
	books, err := s.deps.Turns.Store.Books(ctx)
	if err != nil {
		return nil, err
	}
	out := prrow.TurnFacts(books, reg)
	if !prrow.Started(out) {
		return out, nil
	}
	var agents []turns.AgentObservation
	ok := false
	if s.deps.Agents != nil {
		agents, ok = s.deps.Agents.Agents()
	}
	prrow.Waiting(out, agents, ok, func(handle string) (turn.Session, bool, error) {
		return s.deps.Turns.Registry.Session(ctx, handle)
	}, s.logf)
	return out, nil
}
