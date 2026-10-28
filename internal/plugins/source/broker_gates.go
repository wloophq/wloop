package source

import (
	"context"
	"errors"

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

func (gates) SetOverride(context.Context, plugin.Unit, map[string]string, map[string]string) (bool, error) {
	return false, src.ErrUnsupported
}
