package cli

import (
	"context"
	"errors"
	"fmt"

	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	"github.com/wloophq/wloop/internal/config"
	"github.com/wloophq/wloop/internal/core/automation"
	"github.com/wloophq/wloop/internal/core/outbox"
	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/daemon"
	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/intent/builtin"
)

// serveOutbox serves the drafts on d: published by each built module's Publisher, pinned to their turn's unit,
// approved by the manifests' policies, their evidence written to the items ledger, their effects applied once
// each is sent. The sender runs only with
// jobs, so serve --no-jobs publishes nothing.
func (e *Env) serveOutbox(d daemon.Deps, db *sqlite.DB, cfg config.Config, mods []pluginModule, logf func(string, ...any)) daemon.Deps {
	ledger := e.itemsLedger(db, cfg, logf)
	pin := outboxManifests{loaded: e.loadedIntents, books: envBooks{e}}
	pubs := map[unit.ProviderID]port.Effects{}
	var targets []unit.ProviderID
	for _, m := range mods {
		if m.Publisher != nil {
			pubs[m.ID] = pinnedEffects{Effects: m.Publisher, turns: pin}
			targets = append(targets, m.ID)
		}
	}
	effects := publishers(pubs, cfg)
	e.outbox = &outbox.Service{Store: db.Drafts(), Gates: draftGates{e, db, logf}, Effects: effects, Targets: targets, Policies: e.outboxPolicies(), Facts: e.draftFacts,
		Kinds: outboxKinds, Bases: publisherBases(effects), OnSent: e.draftSentHooks(),
		Posts:  deliveryPosts,
		Broken: e.brokenWorkflows,
		Silent: builtin.SilentEffect,
		Evidence: outbox.EvidenceTo(func(ctx context.Context, prov items.Provenance, ops []items.Op) error {
			_, err := ledger.Apply(ctx, prov, ops)
			return err
		}),
		Clock: clockFunc(e.Now), Log: logf,
		Applier: outbox.EffectApplier{Store: db.DraftEffects(), Clock: clockFunc(e.Now),
			Effects: effectLookup(effectDeps{Nudges: e.newTasks(db, cfg, ledger), Ends: e.newTasks(db, cfg, ledger), Modules: mods, Appliers: e.draftAppliers()})},
		Results: e.newTaskResults(e.newTasks(db, cfg, ledger), mods)}
	e.hosted.setOutbox(e.outbox)
	d.Outbox, d.SendDrafts = e.outbox, !e.noJobs
	if d.Gates == nil {
		d.Gates = e.newGates(db, logf)
		d.GateReader = e.gateReader
	}
	return d
}

// outboxKinds are the draft kinds the plugins' manifests declare; nil checks none.
var outboxKinds outbox.Kinds

// publisherBases read a unit's live basis from the publisher of its provider; a publisher that reads none
// answers "", and a provider with none fails, so no draft is taken as fresh unread.
func publisherBases(effects outbox.Effects) outbox.Bases { return basesOf(effects) }

type basesOf outbox.Effects

func (b basesOf) Live(ctx context.Context, u unit.Unit, kind string) (string, error) {
	fx, err := b(u.Provider)
	if err != nil {
		return "", err
	}
	if r, ok := fx.(outbox.Bases); ok {
		return r.Live(ctx, u, kind)
	}
	return "", nil
}

// draftSentHooks complete, once a draft is sent, the turn that ended on it.
func (e *Env) draftSentHooks() []func(context.Context, draft.Draft) error {
	sent, ok := any(e.Turns).(interface {
		DraftSent(ctx context.Context, d draft.Draft) error
	})
	if !ok || e.Turns == nil {
		return nil
	}
	return []func(context.Context, draft.Draft) error{sent.DraftSent}
}

// deliveryPosts says whether a draft of d reaches people, as the plugins'
// manifests declare it.
func deliveryPosts(d draft.Delivery) bool { return builtin.DeliveryPosts(string(d)) }

var errNoEngine = errors.New("no workflow engine reads the unit's facts")

// draftFacts are u's facts as the sweep's engine reads them, scoped by
// workflow when it is set, for the rules of a turn's draft.
func (e *Env) draftFacts(ctx context.Context, u unit.Unit, workflow string) (draft.Subject, error) {
	if e.engine == nil || e.engine.Units == nil {
		return draft.Subject{}, errNoEngine
	}
	return subjectFacts(ctx, e.engine.Units, e.engine.Keep, u, workflow)
}

// subjectFacts are u's facts as the subject of its provider's slot, scoped
// by workflow through keep, the host's bookkeeping, when it is set.
func subjectFacts(ctx context.Context, units automation.Units, keep any, u unit.Unit, workflow string) (draft.Subject, error) {
	facts, err := units.Facts(ctx, u)
	if err != nil {
		return draft.Subject{}, err
	}
	if sc, ok := keep.(automation.Scoper); ok && workflow != "" {
		if facts, err = sc.Scope(ctx, workflow, u, facts); err != nil {
			return draft.Subject{}, err
		}
	}
	return draft.Subject{Slot: builtin.Slots[u.Provider], Facts: facts}, nil
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

// pinnedEffects refuses a draft whose proposing turn is not in the book of the unit it names, the draft's own
// unless its origin names another, before it is proposed, approved or published.
type pinnedEffects struct {
	port.Effects
	turns turnReader
}

func (p pinnedEffects) Delivers(k draft.Kind) draft.Delivery {
	if dl, ok := p.Effects.(port.Deliverer); ok {
		return dl.Delivers(k)
	}
	_, d := draft.ShapeOf(k)
	return d
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

func (p pinnedEffects) Live(ctx context.Context, u unit.Unit, kind string) (string, error) {
	if b, ok := p.Effects.(outbox.Bases); ok {
		return b.Live(ctx, u, kind)
	}
	return "", nil
}

func (p pinnedEffects) Takes(target string) bool {
	t, ok := p.Effects.(port.Targeter)
	return ok && t.Takes(target)
}

func (p pinnedEffects) Landed(ctx context.Context, d draft.Draft) (unit.Unit, bool, error) {
	if l, ok := p.Effects.(port.Lander); ok {
		return l.Landed(ctx, d)
	}
	return unit.Unit{}, false, nil
}

func (p pinnedEffects) Place(ctx context.Context, d draft.Draft) (draft.Placed, error) {
	if pl, ok := p.Effects.(port.Placer); ok {
		return pl.Place(ctx, d)
	}
	return draft.Placed{}, nil
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
	u := d.Unit
	if !o.Unit.IsZero() {
		u = o.Unit
	}
	_, _, ok, err := p.turns.Turn(ctx, u, o.TurnSeq)
	switch {
	case err != nil:
		return fmt.Errorf("reading turn %d of %s: %w", o.TurnSeq, u.Label(), err)
	case !ok:
		return fmt.Errorf("turn %d is not a turn of %s", o.TurnSeq, u.Label())
	}
	return nil
}
