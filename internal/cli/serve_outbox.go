package cli

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	"github.com/wloophq/wloop/internal/config"
	"github.com/wloophq/wloop/internal/core/automation"
	"github.com/wloophq/wloop/internal/core/instructions"
	"github.com/wloophq/wloop/internal/core/outbox"
	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/daemon"
	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/items"
	dturn "github.com/wloophq/wloop/internal/domain/turn"
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
	for p, fx := range e.testPublishers {
		pubs[p] = pinnedEffects{Effects: fx, turns: pin}
		if !slices.Contains(targets, p) {
			targets = append(targets, p)
		}
	}
	effects := publishers(pubs, cfg)
	e.outbox = &outbox.Service{Store: endingDrafts{Store: db.Drafts(), ended: e.draftEnded, logf: logf}, Gates: draftGates{e, db, logf}, Effects: effects, Targets: targets, Policies: e.outboxPolicies(), Facts: e.draftFacts,
		Kinds: draftKinds, Bases: publisherBases(effects), OnSent: []func(context.Context, draft.Draft) error{e.draftSent, e.ruleSent},
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
	if e.Turns != nil {
		e.Turns.DraftGates, e.Turns.DraftVerdict = turnDrafts{e.outbox}, e.draftVerdict
	}
	e.hosted.setOutbox(e.outbox)
	d.Outbox, d.SendDrafts = e.outbox, !e.noJobs
	if d.Gates == nil {
		d.Gates = e.newGates(db, logf)
		d.GateReader = e.gateReader
	}
	return d
}

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

// draftSent completes the turn that ended on d, now sent.
func (e *Env) draftSent(ctx context.Context, d draft.Draft) error {
	if e.Turns == nil {
		return nil
	}
	return e.Turns.DraftSent(ctx, d)
}

// draftEnded ends the asked turn whose approved answer waited on d, which failed or was dropped.
func (e *Env) draftEnded(ctx context.Context, d draft.Draft) error {
	if e.Turns == nil {
		return nil
	}
	return e.Turns.DraftEnded(ctx, d)
}

// draftVerdict is the record verdict d posted, as the kind's manifest of the plugin that posted it declares.
func (e *Env) draftVerdict(d draft.Draft) string {
	spec, ok := draftKinds.Lookup(string(e.outbox.PublisherOf(d)), string(d.Kind))
	if !ok {
		return ""
	}
	return spec.VerdictOf(d)
}

// resumeDrafts point to the drafts the turns of seq's chain proposed on b's unit that are still held or failed,
// with current the unit's basis now, "" when unknown; none while the daemon serves no outbox.
func (e *Env) resumeDrafts(ctx context.Context, b dturn.Book, seq int64, current string) []instructions.Draft {
	if e.outbox == nil {
		return nil
	}
	u := b.Unit
	ds, err := e.outbox.Store.List(ctx, outbox.Filter{Unit: &u, States: []draft.State{draft.Held, draft.Failed}})
	if err != nil {
		return nil
	}
	var seqs []int64
	for _, t := range b.Chain(seq) {
		seqs = append(seqs, t.Seq)
	}
	ds = slices.DeleteFunc(ds, func(d draft.Draft) bool {
		agent := d.Origin.ByAgent()
		return !agent || !slices.Contains(seqs, d.Origin.TurnSeq)
	})
	return instructions.Pointers(ds, current)
}

// endingDrafts are the outbox's drafts, telling ended each draft that fails or is dropped as it is stored so.
type endingDrafts struct {
	outbox.Store
	ended func(ctx context.Context, d draft.Draft) error
	logf  func(string, ...any)
}

func (s endingDrafts) Update(ctx context.Context, d draft.Draft, from draft.State) (draft.Draft, error) {
	got, err := s.Store.Update(ctx, d, from)
	if err == nil && got.State != from && (got.State == draft.Failed || got.State == draft.Dropped) {
		if err := s.ended(ctx, got); err != nil {
			s.logf("draft %d %s: ending the turn that waited on it: %v", got.ID, got.State, err)
		}
	}
	return got, err
}

// ruleSent tells the user, through the daemon serving now, that a rule of theirs posted d.
func (e *Env) ruleSent(ctx context.Context, d draft.Draft) error {
	if e.linkStates == nil {
		return nil
	}
	if s := e.linkStates.Load(); s != nil {
		return s.RuleSent(ctx, d)
	}
	return nil
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
	return ""
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

func (p pinnedEffects) Within(ctx context.Context, u unit.Unit, target string) (bool, error) {
	if sc, ok := p.Effects.(port.Scoper); ok {
		return sc.Within(ctx, u, target)
	}
	return false, nil
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

// turnDrafts are the outbox's drafts as the turns read them.
type turnDrafts struct{ *outbox.Service }

func (t turnDrafts) Draft(ctx context.Context, id draft.ID) (draft.Draft, bool, error) {
	d, err := t.Store.Get(ctx, id)
	if errors.Is(err, outbox.ErrNotFound) {
		return draft.Draft{}, false, nil
	}
	return d, err == nil, err
}
