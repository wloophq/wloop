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

// prepares fill an intent's call before it runs, or refuse it, and return
// the data of its event.
var prepares = map[string]func(s *Server, ctx context.Context, u unit.Unit, c *Call) (string, error){
	"nudge.prepare": (*Server).prepareNudge,
}

// actionEffect is an on_sent effect of a round asked through the daemon's
// action: before runs ahead of the prepare, after once the round went out
// with the data its prepare returned.
type actionEffect struct {
	name   string
	before func(s *Server, ctx context.Context, u unit.Unit) error
	after  func(s *Server, ctx context.Context, u unit.Unit, data string) error
}

// actionEffects run in this order within each phase.
var actionEffects = []actionEffect{
	{name: "plan.drop_pending", before: func(s *Server, ctx context.Context, u unit.Unit) error {
		return s.dropPending(ctx, u, askedByYou)
	}},
	{name: "event.round_sent", after: func(s *Server, ctx context.Context, u unit.Unit, _ string) error {
		return s.recordEvent(ctx, u, dinv.KindRoundSent, askedByYou)
	}},
	{name: "event.nudged", after: func(s *Server, ctx context.Context, u unit.Unit, data string) error {
		return s.recordEvent(ctx, u, dinv.KindNudged, data)
	}},
}

// actionEffectsOf are the action effects d's on_sent names, in table order.
func actionEffectsOf(d dintent.Def) []actionEffect {
	return slices.DeleteFunc(slices.Clone(actionEffects), func(e actionEffect) bool { return !slices.Contains(d.OnSent, e.name) })
}

// Builtins are the builtin names the daemon implements, by slot.
func Builtins() map[builtin.Slot][]string {
	onSent := make([]string, len(actionEffects))
	for i, e := range actionEffects {
		onSent[i] = e.name
	}
	return map[builtin.Slot][]string{
		builtin.SlotPrepare: slices.Sorted(maps.Keys(prepares)),
		builtin.SlotOnSent:  onSent,
	}
}
