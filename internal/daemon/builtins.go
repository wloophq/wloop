package daemon

import (
	"context"
	"maps"
	"slices"

	dintent "github.com/wloophq/wloop/internal/domain/intent"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/intent/builtin"
)

// prepares fill an intent's call before it runs, or refuse it.
var prepares = map[string]func(s *Server, ctx context.Context, u unit.Unit, c *Call) error{
	builtin.PrepareNudge: (*Server).prepareNudge,
}

// actionEffect is a turn effect of a round asked through the daemon's
// action: before runs ahead of the prepare, after once the round went out.
type actionEffect struct {
	name   string
	before func(s *Server, ctx context.Context, u unit.Unit) error
	after  func(s *Server, ctx context.Context, u unit.Unit) error
}

// actionEffects run in this order within each phase.
var actionEffects = []actionEffect{
	{name: builtin.EffectPlanCleared, before: func(s *Server, ctx context.Context, u unit.Unit) error {
		return s.dropPending(ctx, u, askedByYou)
	}},
	{name: builtin.EffectPRRoundSent, after: func(s *Server, ctx context.Context, u unit.Unit) error {
		return s.recordEvent(ctx, u, dinv.KindRoundSent, askedByYou)
	}},
}

// actionEffectsOf are the action effects d's effects name, in table order.
func actionEffectsOf(d dintent.Def) []actionEffect {
	return slices.DeleteFunc(slices.Clone(actionEffects), func(e actionEffect) bool { return !slices.Contains(d.Effects, e.name) })
}

// Implemented are the builtins the daemon implements: the prepares a role
// binds and the turn effects it applies.
type Implemented struct{ Prepares, Effects []string }

// Builtins are the builtins the daemon implements.
func Builtins() Implemented {
	effects := make([]string, len(actionEffects))
	for i, e := range actionEffects {
		effects[i] = e.name
	}
	return Implemented{Prepares: slices.Sorted(maps.Keys(prepares)), Effects: effects}
}
