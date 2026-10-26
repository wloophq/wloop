package cli

import (
	"context"
	"time"

	"github.com/wloophq/wloop/internal/core/outbox"
	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/gtd"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/intent/builtin"
	"github.com/wloophq/wloop/internal/source"
)

// effectNudges stamps a sent nudge on its waiting entries (*coregtd.Service).
type effectNudges interface {
	Nudged(ctx context.Context, ids []gtd.ID, u unit.Unit, at time.Time) error
}

// effectDeps are what the draft effects act through.
type effectDeps struct {
	Nudges  effectNudges // nil stamps nothing
	Modules []source.Module
}

// draftEffects are the draft effects the daemon applies once a draft is
// sent, by name; a plugin's own come from its Module.OnPosted and win.
func draftEffects(d effectDeps) map[string]outbox.Effect {
	return map[string]outbox.Effect{
		"entry.nudged": d.entryNudged,
		"unit.closed":  noPluginClosure,
		"pr.nudged":    appliedByPublisher,
	}
}

// appliedByPublisher is an effect its provider's publisher applies as it
// posts, so applying it after the send does nothing.
func appliedByPublisher(context.Context, draft.Draft) error { return nil }

// noPluginClosure is unit.closed on a unit whose plugin records no closure.
func noPluginClosure(context.Context, draft.Draft) error { return nil }

// entryNudged stamps the draft's entry, or every live waiting entry of its
// unit when it names none.
func (d effectDeps) entryNudged(ctx context.Context, dr draft.Draft) error {
	if d.Nudges == nil {
		return nil
	}
	var ids []gtd.ID
	if dr.Entry != "" {
		ids = []gtd.ID{gtd.ID(dr.Entry)}
	}
	at := dr.Evidence.At
	if at.IsZero() {
		at = dr.UpdatedAt
	}
	return d.Nudges.Nudged(ctx, ids, dr.Unit, at)
}

// effectLookup finds an effect for a draft of provider p: its plugin's
// own, else the daemon's; none when the effect belongs to another provider.
func effectLookup(d effectDeps) outbox.EffectLookup {
	core := draftEffects(d)
	return func(p unit.ProviderID, name string) (outbox.Effect, bool) {
		for _, e := range builtin.Effects {
			if e.Name == name && e.Provider != "" && e.Provider != p {
				return nil, false
			}
		}
		for _, m := range d.Modules {
			if fx, ok := m.OnPosted[name]; ok && m.ID == p {
				return fx, true
			}
		}
		fx, ok := core[name]
		return fx, ok
	}
}
