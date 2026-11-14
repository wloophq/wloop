package daemon

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/wloophq/wloop/internal/api"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/intent/builtin"
	src "github.com/wloophq/wloop/pkg/plugin/source"
)

// prepare runs d's prepare on u, the plugin action it names, which fills
// the call's inputs, normalized to d's, or refuses the call.
func (s *Server) prepare(ctx context.Context, d dintent.Def, u unit.Unit, c *Call) error {
	name, ok := strings.CutPrefix(d.Prepare, builtin.PrepareAction)
	if !ok || name == "" {
		return fmt.Errorf("%s: the daemon has no prepare %s", d.Name, d.Prepare)
	}
	res, err := s.askPlugin(ctx, name, u, api.PluginActionArgs{Request: api.ActionRequest{Guidance: c.Guidance}, Inputs: c.Inputs})
	if err != nil {
		return err
	}
	filled, err := d.NormalizeAll(res.Inputs)
	if err != nil {
		return fmt.Errorf("%s: prepare %s: %w", d.Name, d.Prepare, err)
	}
	if c.Inputs == nil {
		c.Inputs = map[string]any{}
	}
	maps.Copy(c.Inputs, filled)
	return nil
}

// preparedInputs are inputs with what d's prepare on u filled.
func (s *Server) preparedInputs(ctx context.Context, d dintent.Def, u unit.Unit, guidance string, inputs map[string]any) (map[string]any, error) {
	c := Call{Guidance: guidance, Inputs: maps.Clone(inputs)}
	if err := s.prepare(ctx, d, u, &c); err != nil {
		return nil, err
	}
	return c.Inputs, nil
}

// PrepareTurn is inputs with what the prepare of intent's role filled on u,
// the same an action or a turn by hand of it runs; inputs as given when it
// has none. A refusal wraps src.ErrUnfit and a plugin out of reach
// src.ErrUnavailable.
func (s *Server) PrepareTurn(ctx context.Context, intent string, u unit.Unit, guidance string, inputs map[string]any) (map[string]any, error) {
	d, err := intentDef(s.loaded(), intent)
	if err != nil || d.Prepare == "" {
		return inputs, err
	}
	filled, err := s.preparedInputs(ctx, d, u, guidance, inputs)
	var unfit badRequest
	var down unavailable
	switch {
	case errors.As(err, &unfit):
		return nil, fmt.Errorf("%w: %s", src.ErrUnfit, unfit.msg)
	case errors.As(err, &down):
		return nil, fmt.Errorf("%w: %s", src.ErrUnavailable, down.msg)
	}
	return filled, err
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
	{name: builtin.EffectRoundSent, after: func(s *Server, ctx context.Context, u unit.Unit) error {
		return s.recordEvent(ctx, u, builtin.EventRoundSent, askedByYou)
	}},
}

// actionEffectsOf are the action effects d's effects name, in table order.
func actionEffectsOf(d dintent.Def) []actionEffect {
	return slices.DeleteFunc(slices.Clone(actionEffects), func(e actionEffect) bool { return !slices.Contains(d.Effects, e.name) })
}

// Implemented are the builtins the daemon implements: the turn effects it
// applies. A role's prepare is its plugin's action (builtin.PrepareAction).
type Implemented struct{ Effects []string }

// Builtins are the builtins the daemon implements.
func Builtins() Implemented {
	effects := make([]string, len(actionEffects))
	for i, e := range actionEffects {
		effects[i] = e.name
	}
	return Implemented{Effects: effects}
}
