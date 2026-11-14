package daemon

import (
	"context"
	"errors"
	"fmt"
	"maps"
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
// has none. A refusal, or a plugin that is off, wraps src.ErrUnfit and a
// plugin out of reach src.ErrUnavailable, each keeping the daemon's error,
// so an action answers it with the status a prepare's refusal has.
func (s *Server) PrepareTurn(ctx context.Context, intent string, u unit.Unit, guidance string, inputs map[string]any) (map[string]any, error) {
	d, err := intentDef(s.loaded(), intent)
	if err != nil || d.Prepare == "" {
		return inputs, err
	}
	filled, err := s.preparedInputs(ctx, d, u, guidance, inputs)
	var unfit badRequest
	var off notFound
	var down unavailable
	switch {
	case errors.As(err, &unfit):
		return nil, fmt.Errorf("%w: %w", src.ErrUnfit, unfit)
	case errors.As(err, &off):
		return nil, fmt.Errorf("%w: %w", src.ErrUnfit, off)
	case errors.As(err, &down):
		return nil, fmt.Errorf("%w: %w", src.ErrUnavailable, down)
	}
	return filled, err
}
