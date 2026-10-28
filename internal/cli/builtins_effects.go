package cli

import (
	"context"
	"slices"
	"time"

	psource "github.com/wloophq/wloop/internal/plugins/source"
	src "github.com/wloophq/wloop/pkg/plugin/source"

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
	// Appliers apply each provider's plugin effects; nil applies none.
	Appliers func(p unit.ProviderID) (effectApplier, bool)
}

// draftEffects are the draft effects the daemon applies once a draft is
// sent, by name; a plugin's own come from its Module.OnPosted and win.
func draftEffects(d effectDeps) map[string]outbox.Effect {
	return map[string]outbox.Effect{
		builtin.EffectTaskNudged: d.taskNudged,
		builtin.EffectUnitClosed: noPluginClosure,
	}
}

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

// effectLookup finds an effect for a draft of provider p: its built
// module's own, else the daemon's, else its plugin's ApplyEffect; none when
// the effect belongs to another provider, or applies through a plugin that
// p has not built or that does not run.
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
		fx, ok := core[name]
		if i < 0 || !builtin.Effects[i].Plugin {
			return fx, ok
		}
		if ok {
			return fx, built
		}
		if d.Appliers == nil {
			return nil, false
		}
		a, ok := d.Appliers(p)
		if !ok {
			return nil, false
		}
		return func(ctx context.Context, dr draft.Draft) error {
			w := psource.WireDraft(dr)
			return a.ApplyEffect(ctx, src.EffectRequest{Effect: name, Unit: w.Unit, Draft: &w})
		}, true
	}
}
