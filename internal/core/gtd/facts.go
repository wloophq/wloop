package gtd

import (
	"context"

	"github.com/wloophq/wloop/internal/domain/card"
	"github.com/wloophq/wloop/internal/domain/gtd"
	"github.com/wloophq/wloop/internal/domain/unit"
)

// CardFacts reads a unit's facts from its stored card (#unit-card).
type CardFacts struct {
	Cards card.Store
	// Enabled says whether a provider's plugin is enabled; nil: every one is.
	Enabled func(unit.ProviderID) bool
	// Me is the user's identity now; nil: nobody.
	Me func() gtd.Me
}

var _ Facts = CardFacts{}

// UnitFacts is u's facts from its card; ok is false for a disabled plugin's unit. A unit with no card stored
// has no facts beyond its identity.
func (f CardFacts) UnitFacts(ctx context.Context, u unit.Unit) (gtd.UnitFacts, bool, error) {
	if f.Enabled != nil && !f.Enabled(u.Provider) {
		return gtd.UnitFacts{}, false, nil
	}
	out := gtd.UnitFacts{Unit: u}
	if f.Cards == nil {
		return out, true, nil
	}
	got, err := f.Cards.Get(ctx, []unit.Unit{u})
	if err != nil {
		return gtd.UnitFacts{}, false, err
	}
	snap, ok := got[u]
	if !ok {
		return out, true, nil
	}
	c := snap.Card
	out.StartedByMe, out.ClosurePosted, out.Cares = c.StartedByMe, c.ClosurePosted, c.Cares
	var me gtd.Me
	if f.Me != nil {
		me = f.Me()
	}
	if h := c.LastHuman; h != nil && !h.Who.Is(me) {
		out.LastHuman = h.At
	}
	if c.Mute != nil {
		out.Mute = &gtd.Mute{Until: c.Mute.Until, UntilStateChange: c.Mute.UntilStateChange}
	}
	return out, true, nil
}
