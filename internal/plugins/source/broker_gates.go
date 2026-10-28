package source

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/wloophq/wloop/internal/core/port"

	"github.com/wloophq/wloop/internal/domain/unit"

	"github.com/wloophq/wloop/pkg/plugin"
	src "github.com/wloophq/wloop/pkg/plugin/source"
)

type gates struct{ b *Broker }

func (gates) OpenGate(context.Context, src.GateRequest) (src.GateRef, error) {
	return src.GateRef{}, src.ErrUnsupported
}

func (gates) RecordSent(context.Context, src.Draft, string, string) (src.GateRef, error) {
	return src.GateRef{}, src.ErrUnsupported
}

// RehomeDrafts moves the drafts of each move's From onto its To, both the
// plugin's own units, in one host transaction.
func (g gates) RehomeDrafts(ctx context.Context, moves []src.UnitMove) (int, error) {
	if g.b.Drafts == nil {
		return 0, src.ErrUnsupported
	}
	byFrom := make(map[unit.Unit]unit.Unit, len(moves))
	var errs []error
	for _, m := range moves {
		from, err := g.b.Units.Own(g.b.ID, m.From)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		to, err := g.b.Units.Own(g.b.ID, m.To)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		byFrom[from] = to
	}
	if err := errors.Join(errs...); err != nil {
		return 0, err
	}
	return g.b.Drafts.RehomeDrafts(ctx, byFrom)
}

// SetOverride sets the host overrides of one of the plugin's own units a
// plugin may set (port.PluginOverrides), compare-and-set over ifMatch in
// one host transaction. It takes no loop lock.
func (g gates) SetOverride(ctx context.Context, w plugin.Unit, set, ifMatch map[string]string) (bool, error) {
	if g.b.Overrides == nil {
		return false, src.ErrUnsupported
	}
	u, err := g.b.Units.Own(g.b.ID, w)
	if err != nil {
		return false, err
	}
	for n := range set {
		if !slices.Contains(port.PluginOverrides, n) {
			return false, fmt.Errorf("plugin %s sets override %q, which is not one of %v", g.b.ID, n, port.PluginOverrides)
		}
	}
	for n := range ifMatch {
		if !slices.Contains(port.Overrides, n) {
			return false, fmt.Errorf("plugin %s matches override %q, which is not one of %v", g.b.ID, n, port.Overrides)
		}
	}
	return g.b.Overrides.SetOverride(ctx, u, set, ifMatch)
}
