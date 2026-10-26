package cli

import (
	"context"
	"errors"
	"fmt"

	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	"github.com/wloophq/wloop/internal/config"
	"github.com/wloophq/wloop/internal/core/outbox"
	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/daemon"
	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/source"
)

// serveOutbox serves the drafts on d: published by each built module's Publisher, pinned to their turn's unit,
// approved by the manifests' policies, their evidence written to the items ledger, their effects applied once
// each is sent. The sender runs only with
// jobs, so serve --no-jobs publishes nothing.
func (e *Env) serveOutbox(d daemon.Deps, db *sqlite.DB, cfg config.Config, mods []source.Module, logf func(string, ...any)) daemon.Deps {
	ledger := e.itemsLedger(db, cfg, logf)
	pin := outboxManifests{loaded: e.loadedIntents, books: envBooks{e}}
	pubs := map[unit.ProviderID]port.Effects{}
	for _, m := range mods {
		if m.Publisher != nil {
			pubs[m.ID] = pinnedEffects{Effects: m.Publisher, turns: pin}
		}
	}
	e.outbox = &outbox.Service{Store: db.Drafts(), Effects: publishers(pubs, cfg), Policies: e.outboxPolicies(), Facts: e.draftFacts,
		Evidence: outbox.EvidenceTo(func(ctx context.Context, prov items.Provenance, ops []items.Op) error {
			_, err := ledger.Apply(ctx, prov, ops)
			return err
		}),
		Items: ledger, Clock: clockFunc(e.Now), Log: logf,
		Applier: outbox.EffectApplier{Store: db.DraftEffects(), Clock: clockFunc(e.Now),
			Effects: effectLookup(effectDeps{Nudges: e.entries(db, cfg, ledger), Modules: mods})}}
	d.Outbox, d.SendDrafts = e.outbox, !e.noJobs
	return d
}

var errNoEngine = errors.New("no workflow engine reads the unit's facts")

// draftFacts are u's facts as the sweep's engine reads them, for the rules
// of a turn's draft.
func (e *Env) draftFacts(ctx context.Context, u unit.Unit) (map[string]any, error) {
	if e.engine == nil || e.engine.Units == nil {
		return nil, errNoEngine
	}
	return e.engine.Units.Facts(ctx, u)
}

// publishers is the Effects of pubs: a provider with none is disabled, or publishes nothing.
func publishers(pubs map[unit.ProviderID]port.Effects, cfg config.Config) outbox.Effects {
	return func(p unit.ProviderID) (port.Effects, error) {
		if fx, ok := pubs[p]; ok {
			return fx, nil
		}
		if !cfg.PluginEnabled(string(p)) {
			return nil, fmt.Errorf("%w: the %s plugin is disabled", outbox.ErrDisabled, p)
		}
		return nil, fmt.Errorf("%w: %s publishes nothing", outbox.ErrDisabled, p)
	}
}

type turnReader interface {
	Turn(ctx context.Context, u unit.Unit, seq int64) (intent, workflow string, ok bool, err error)
}

// pinnedEffects refuses a draft a turn proposed on a unit that is not the turn's own, before it is proposed,
// approved or published.
type pinnedEffects struct {
	port.Effects
	turns turnReader
}

func (p pinnedEffects) Validate(ctx context.Context, d draft.Draft) error {
	if err := p.pinned(ctx, d); err != nil {
		return err
	}
	if v, ok := p.Effects.(port.Validator); ok {
		return v.Validate(ctx, d)
	}
	return nil
}

func (p pinnedEffects) Where(ctx context.Context, d draft.Draft) (string, error) {
	l, ok := p.Effects.(port.Locator)
	if !ok {
		return "", errors.New("the publisher names no destination")
	}
	return l.Where(ctx, d)
}

func (p pinnedEffects) Publish(ctx context.Context, d draft.Draft) (draft.Evidence, error) {
	if err := p.pinned(ctx, d); err != nil {
		return draft.Evidence{}, err
	}
	return p.Effects.Publish(ctx, d)
}

func (p pinnedEffects) pinned(ctx context.Context, d draft.Draft) error {
	o := d.Origin
	switch {
	case o.Kind == draft.OriginTurn && o.TurnSeq <= 0:
		return fmt.Errorf("draft %d is a turn's and names no turn", d.ID)
	case o.Kind != draft.OriginTurn && (o.Kind != draft.OriginSession || o.TurnSeq <= 0):
		return nil
	}
	_, _, ok, err := p.turns.Turn(ctx, d.Unit, o.TurnSeq)
	switch {
	case err != nil:
		return fmt.Errorf("reading turn %d of %s: %w", o.TurnSeq, d.Unit.Label(), err)
	case !ok:
		return fmt.Errorf("turn %d is not a turn of %s: a turn's draft posts only on its own unit", o.TurnSeq, d.Unit.Label())
	}
	return nil
}
