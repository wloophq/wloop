package cli

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"sync/atomic"

	"github.com/wloophq/wloop/internal/core/lock"
	"github.com/wloophq/wloop/internal/core/turns"
	"github.com/wloophq/wloop/internal/daemon"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	dturn "github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/intent/builtin"
	src "github.com/wloophq/wloop/pkg/plugin/source"
)

// turnStartDeps are what turn.start reads on units outside the round
// providers: the headless runner and the count of automated turns.
type turnStartDeps struct {
	Headless headlessStarter
	Turns    automatedTurns
}

// wireTurnStart wires turn.start on units other than pull requests and its daily budget.
func (e *Env) wireTurnStart(w *serveWiring) {
	if w.Deps != nil && w.Deps.Headless != nil {
		w.Engine.TurnStart.Headless = w.Deps.Headless
	}
	if w.DB != nil {
		w.Engine.TurnStart.Turns = w.DB
	}
}

// turnStarter is the use case every new turn starts through, over the
// loaded intents, the guidance kept on the units, the loop mutex mu and the
// turn effects; plan is the daemon's plan once built. It has no prepare.
func (e *Env) turnStarter(mu *lock.Mutex, plan *latePlan, logf func(string, ...any)) *turns.Starter {
	return &turns.Starter{Defs: loadedDefs{e}, Kept: recGuidance{e}, Loop: serveLoop{e: e, mu: mu},
		Effects: startEffects{e: e, plan: plan}, Store: e.Turns.Store, Guides: profileGuides{e}, Log: logf}
}

// profileGuides are the guides of the loaded profile.
type profileGuides struct{ e *Env }

func (g profileGuides) Ready(d turns.Def) error { return g.e.guidesReady(d.Def) }

// loadedDefs are the loaded intents as a turn's start reads them.
type loadedDefs struct{ e *Env }

func (d loadedDefs) Def(name dturn.Intent) (turns.Def, bool) {
	l := d.e.loadedIntents()
	if l == nil {
		return turns.Def{}, false
	}
	def, ok := l.Registry.Get(dintent.Name(name))
	return startDef(def), ok
}

func startDef(d dintent.Def) turns.Def {
	return turns.Def{Def: d, HandsGuidance: slices.Contains(d.Effects, builtin.EffectGuidanceHanded)}
}

// servedStarts is the starter every server a serve runs shares, so that an
// action one server delegated, still running after the next is built, reads
// no field the next one writes; the plan and the prepares are those of the
// server built last.
type servedStarts struct {
	starter  *turns.Starter
	plan     latePlan
	prepares daemonPrepares
}

// servedStarts is e's served starter, built over mu on the first call and
// made e's turn starter.
func (e *Env) servedStarts(mu *lock.Mutex, logf func(string, ...any)) *servedStarts {
	if e.starts == nil {
		s := &servedStarts{}
		s.starter = e.turnStarter(mu, &s.plan, logf)
		s.starter.Preparer = startPreparer{&s.prepares}
		e.starts = s
	}
	if e.Turns.Starter != e.starts.starter {
		e.Turns.Starter = e.starts.starter
	}
	return e.starts
}

// newDaemon is the daemon serve runs over d, which runs the prepares of the
// turns the served starter starts.
func (e *Env) newDaemon(d daemon.Deps) *daemon.Server {
	srv := daemon.New(d)
	if e.starts != nil {
		e.starts.prepares.srv.Store(srv)
	}
	return srv
}

// daemonPrepares are the prepares of the daemon newDaemon built last; a
// prepare before it builds one is turns.ErrTransient.
type daemonPrepares struct{ srv atomic.Pointer[daemon.Server] }

func (p *daemonPrepares) PrepareTurn(ctx context.Context, intent string, u unit.Unit, guidance string, inputs map[string]any) (map[string]any, error) {
	srv := p.srv.Load()
	if srv == nil {
		return nil, fmt.Errorf("%s: %w: no daemon runs the prepare of %s yet", u.Label(), turns.ErrTransient, intent)
	}
	return srv.PrepareTurn(ctx, intent, u, guidance, inputs)
}

// startPreparer is a prepare whose refusal is the start's: a refusal is
// turns.ErrRefused and a prepare out of reach turns.ErrTransient, each
// keeping its plugin's error and text.
type startPreparer struct{ p turns.Preparer }

func (p startPreparer) PrepareTurn(ctx context.Context, intent string, u unit.Unit, guidance string, inputs map[string]any) (map[string]any, error) {
	filled, err := p.p.PrepareTurn(ctx, intent, u, guidance, inputs)
	switch {
	case errors.Is(err, src.ErrUnfit):
		return nil, admissionError{kind: turns.ErrRefused, err: err}
	case errors.Is(err, src.ErrUnavailable):
		return nil, admissionError{kind: turns.ErrTransient, err: err}
	}
	return filled, err
}

// admissionError is err read as kind too.
type admissionError struct{ kind, err error }

func (e admissionError) Error() string   { return e.err.Error() }
func (e admissionError) Unwrap() []error { return []error{e.kind, e.err} }

// serveLoop is the daemon's loop mutex, taken once what a turn of the unit
// reads of its pull request is read ahead.
type serveLoop struct {
	e  *Env
	mu *lock.Mutex
}

func (l serveLoop) Enter(ctx context.Context, u unit.Unit) (context.Context, func(), error) {
	if ref, err := githubRef(u); err == nil {
		ctx = l.e.ReadAhead(ctx, ref, false)
	}
	if err := l.mu.LockContext(ctx); err != nil {
		return ctx, nil, err
	}
	return ctx, l.mu.Unlock, nil
}

// latePlan is the daemon's plan, which serve builds after the starter.
type latePlan struct{ p atomic.Pointer[daemon.Plan] }

func (l *latePlan) set(p daemon.Plan) { l.p.Store(&p) }

func (l *latePlan) get() (daemon.Plan, bool) {
	if l == nil {
		return nil, false
	}
	p := l.p.Load()
	if p == nil || *p == nil {
		return nil, false
	}
	return *p, true
}

// askedByYou is why a turn a person asked for outside a session drops the
// unit's pending plan items and records its round.
const askedByYou = "asked by you"

// hostEffect is a turn effect the host applies, with what it does in each
// phase of a person's turn; every other turn effect is its plugin's, applied
// through ApplyEffect.
type hostEffect struct {
	name string
	// before runs before the turn goes out.
	before func(f startEffects, ctx context.Context, u unit.Unit) error
	// sent runs once the turn went out.
	sent func(f startEffects, ctx context.Context, o turns.TurnOut)
	// started is an effect the start itself applies, read from the intent
	// as turns.Def.
	started bool
}

// hostEffects are the host's turn effects, in the order each phase runs them.
var hostEffects = []hostEffect{
	{name: builtin.EffectPlanCleared, before: func(f startEffects, ctx context.Context, u unit.Unit) error {
		p, ok := f.plan.get()
		if !ok {
			return nil
		}
		return p.Drop(ctx, u, askedByYou)
	}},
	{name: builtin.EffectGuidanceHanded, started: true},
	{name: builtin.EffectRoundSent, sent: func(f startEffects, ctx context.Context, o turns.TurnOut) {
		ref, err := githubRef(o.Unit)
		if err != nil {
			return
		}
		why := cmp.Or(o.Reason, askedByYou)
		if o.Reason == "" && o.Route == src.RouteSession {
			why = handInSession
		}
		f.e.recordEvent(ctx, ref, dinv.KindRoundSent, withInstructions(why, o.Outcome.ID))
	}},
}

// hostEffectsOf are the host's turn effects d names, in table order.
func hostEffectsOf(d dintent.Def) []hostEffect {
	return slices.DeleteFunc(slices.Clone(hostEffects), func(h hostEffect) bool { return !slices.Contains(d.Effects, h.name) })
}

func hostEffectNames() []string {
	names := make([]string, len(hostEffects))
	for i, h := range hostEffects {
		names[i] = h.name
	}
	return names
}

// startEffects are the turn effects a start applies: the plugin's through
// its ApplyEffect, and the host's.
type startEffects struct {
	e    *Env
	plan *latePlan
}

func (f startEffects) Check(_ context.Context, d turns.Def, u unit.Unit) error {
	return f.e.checkEffects(d.Def, u)
}

func (f startEffects) Before(ctx context.Context, d turns.Def, u unit.Unit) error {
	for _, h := range hostEffectsOf(d.Def) {
		if h.before == nil {
			continue
		}
		if err := h.before(f, ctx, u); err != nil {
			return err
		}
	}
	return nil
}

func (f startEffects) Recorded(ctx context.Context, o turns.TurnOut) error {
	f.e.applyPluginEffects(ctx, o.Def.Def, o.Unit, src.EffectRequest{Turn: &src.TurnRef{ID: o.Outcome.ID, Head: o.Outcome.Head}, Route: o.Route,
		Taken: o.Outcome.Submitted, Planned: o.Planned}, o.Inputs)
	return nil
}

func (f startEffects) Sent(ctx context.Context, o turns.TurnOut) error {
	for _, h := range hostEffectsOf(o.Def.Def) {
		if h.sent != nil {
			h.sent(f, ctx, o)
		}
	}
	return nil
}

func (f startEffects) Handed(ctx context.Context, o turns.TurnOut) error {
	if ref, err := githubRef(o.Unit); err == nil {
		f.e.recordGuidance(ctx, ref, o.Guidance)
	}
	return nil
}
