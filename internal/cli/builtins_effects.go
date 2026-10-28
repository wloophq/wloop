package cli

import (
	"context"
	"slices"
	"time"

	"github.com/wloophq/wloop/internal/core/outbox"
	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/intent/builtin"
	"github.com/wloophq/wloop/internal/source"
)

// effectNudges stamps a sent nudge on its waiting entries (*coretasks.Service).
type effectNudges interface {
	Nudged(ctx context.Context, ids []task.ID, u unit.Unit, at time.Time) error
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
		builtin.EffectTaskNudged: d.taskNudged,
		builtin.EffectUnitClosed: noPluginClosure,
		builtin.EffectPRNudged:   appliedByPublisher,
	}
}

// appliedByPublisher is an effect its provider's publisher applies as it
// posts, so applying it after the send does nothing.
func appliedByPublisher(context.Context, draft.Draft) error { return nil }

// noPluginClosure is unit.closed on a unit whose built plugin records no closure.
func noPluginClosure(context.Context, draft.Draft) error { return nil }

// taskNudged stamps the draft's entry, or every live waiting entry of its
// unit when it names none.
func (d effectDeps) taskNudged(ctx context.Context, dr draft.Draft) error {
	if d.Nudges == nil {
		return nil
	}
	var ids []task.ID
	if dr.Task != "" {
		ids = []task.ID{task.ID(dr.Task)}
	}
	at := dr.Evidence.At
	if at.IsZero() {
		at = dr.UpdatedAt
	}
	return d.Nudges.Nudged(ctx, ids, dr.Unit, at)
}

// effectLookup finds an effect for a draft of provider p: its plugin's
// own, else the daemon's; none when the effect belongs to another provider,
// or applies through a plugin p has not built.
func effectLookup(d effectDeps) outbox.EffectLookup {
	core := draftEffects(d)
	return func(p unit.ProviderID, name string) (outbox.Effect, bool) {
		i := slices.IndexFunc(builtin.Effects, func(e builtin.Effect) bool { return e.Name == name })
		if i >= 0 && builtin.Effects[i].Provider != "" && builtin.Effects[i].Provider != p {
			return nil, false
		}
		built := false
		for _, m := range d.Modules {
			if m.ID != p {
				continue
			}
			built = true
			if fx, ok := m.OnPosted[name]; ok {
				return fx, true
			}
		}
		if i >= 0 && builtin.Effects[i].Plugin && !built {
			return nil, false
		}
		fx, ok := core[name]
		return fx, ok
	}
}
