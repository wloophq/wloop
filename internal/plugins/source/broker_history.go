package source

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/wloophq/wloop/internal/domain/unit"
	src "github.com/wloophq/wloop/pkg/plugin/source"
)

type history struct{ b *Broker }

// Events reads the host's events of the plugin's own units, the ones it
// recorded and the loop's; a unit of another provider refuses the query.
func (h history) Events(ctx context.Context, q src.EventQuery) ([]src.Event, error) {
	if h.b.Events == nil {
		return nil, src.ErrUnsupported
	}
	units := make([]unit.Unit, 0, len(q.Units))
	var errs []error
	for _, w := range q.Units {
		u, err := h.b.Units.Own(h.b.ID, w)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		units = append(units, u)
	}
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	evs, err := h.b.Events.UnitEvents(ctx, units, q.Kinds, q.Since)
	if err != nil {
		return nil, err
	}
	out := make([]src.Event, len(evs))
	for i, e := range evs {
		out[i] = src.Event{Unit: Wire(e.Unit), Kind: e.Kind, Key: e.Key, Actor: e.Actor, At: e.At}
		if e.Anchor != (unit.Anchor{}) {
			out[i].Anchor = wireAnchor(e.Anchor)
		}
		if e.Data != "" {
			out[i].Data = json.RawMessage(e.Data)
		}
	}
	return out, nil
}
