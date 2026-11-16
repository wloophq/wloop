package tasks

import (
	"context"
	"slices"

	"github.com/wloophq/wloop/internal/domain/card"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
)

// CardFacts reads a unit's facts from its stored card (#unit-card).
type CardFacts struct {
	Cards card.Store
	// Enabled says whether a provider's plugin is enabled; nil: every one is.
	Enabled func(unit.ProviderID) bool
	// Me is the user's identity now; nil: nobody.
	Me func() task.Me
	// Kind is the value of the kind property a provider's plugin gives the tasks of its units; nil: none.
	Kind func(unit.ProviderID) string
}

var (
	_ Facts      = CardFacts{}
	_ UnitsFacts = CardFacts{}
)

// UnitFacts is u's facts from its card; ok is false for a disabled plugin's unit. A unit with no card stored
// has no facts beyond its identity.
func (f CardFacts) UnitFacts(ctx context.Context, u unit.Unit) (task.UnitFacts, bool, error) {
	if !f.enabled(u) {
		return task.UnitFacts{}, false, nil
	}
	if f.Cards == nil {
		return f.facts(u, nil), true, nil
	}
	got, err := f.cards(ctx, u)
	if err != nil {
		return task.UnitFacts{}, false, err
	}
	return f.facts(u, got), true, nil
}

// UnitsFacts is UnitFacts of each of units, their cards read at once; a disabled plugin's unit is absent.
func (f CardFacts) UnitsFacts(ctx context.Context, units []unit.Unit) (map[unit.Unit]task.UnitFacts, error) {
	on := slices.DeleteFunc(slices.Clone(units), func(u unit.Unit) bool { return !f.enabled(u) })
	var got map[unit.Unit]card.Snapshot
	if f.Cards != nil && len(on) > 0 {
		var err error
		if got, err = f.cardsOf(ctx, on); err != nil {
			return nil, err
		}
	}
	out := make(map[unit.Unit]task.UnitFacts, len(on))
	for _, u := range on {
		out[u] = f.facts(u, got)
	}
	return out, nil
}

func (f CardFacts) enabled(u unit.Unit) bool { return f.Enabled == nil || f.Enabled(u.Provider) }

// facts are u's facts from its card in cards.
func (f CardFacts) facts(u unit.Unit, cards map[unit.Unit]card.Snapshot) task.UnitFacts {
	out := task.UnitFacts{Unit: u}
	if f.Kind != nil {
		out.Kind = f.Kind(u.Provider)
	}
	snap, ok := cards[u]
	if !ok {
		return out
	}
	c := snap.Card
	out.StartedByMe, out.ClosurePosted, out.Cares, out.Carded = c.StartedByMe, c.ClosurePosted, c.Cares, true
	var me task.Me
	if f.Me != nil {
		me = f.Me()
	}
	if h := c.LastHuman; h != nil && !h.Who.Is(me) {
		out.LastHuman = h.At
	}
	return out
}

// cards is u's card; with every card of the pass ctx carries, read once, when u is a unit of it.
func (f CardFacts) cards(ctx context.Context, u unit.Unit) (map[unit.Unit]card.Snapshot, error) {
	return f.cardsOf(ctx, []unit.Unit{u})
}

// cardsOf are the cards of units; with every card of the pass ctx carries, read once, when it holds them all.
func (f CardFacts) cardsOf(ctx context.Context, units []unit.Unit) (map[unit.Unit]card.Snapshot, error) {
	if pass, ok := PassOf(ctx); ok && !slices.ContainsFunc(units, func(u unit.Unit) bool { return !pass.Holds(u) }) {
		return Once(ctx, "cards", func() (map[unit.Unit]card.Snapshot, error) { return f.Cards.Get(ctx, pass.Units()) })
	}
	return f.Cards.Get(ctx, units)
}
