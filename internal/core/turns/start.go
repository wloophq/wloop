package turns

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"maps"
	"sync/atomic"

	"github.com/wloophq/wloop/internal/core/lock"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	"github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
)

var (
	// ErrRefused is a start the unit's state refuses, such as a prepare that
	// finds nothing to do; no retry of the same request passes.
	ErrRefused = errors.New("the turn is refused")
	// ErrTransient is a start that may pass on a retry: the prepare's plugin,
	// or the daemon that runs it, is out of reach.
	ErrTransient = errors.New("the turn cannot start now")
	// ErrNoTurn is a Turn no Start or Resume made.
	ErrNoTurn = errors.New("no turn was admitted")
	// ErrUsedTurn is a Turn already used once.
	ErrUsedTurn = errors.New("the turn was already used")
	// ErrAnswered is a Guard that answered the request itself, starting no
	// turn: the guidance given with it is dropped and the kept one stays.
	ErrAnswered = errors.New("the request was answered without a turn")
)

// Def is an intent as Start reads it.
type Def struct {
	dintent.Def
	// HandsGuidance is the intent's turn.guidance_handed: the guidance kept
	// for the unit's next turn goes to its turns, and comes back when one
	// does not go out.
	HandsGuidance bool
}

// Defs are the loaded intents.
type Defs interface {
	Def(name turn.Intent) (Def, bool)
}

// Preparer runs the prepare of an intent's role, which fills the turn's
// inputs; a refusal wraps ErrRefused, a prepare out of reach ErrTransient.
type Preparer interface {
	PrepareTurn(ctx context.Context, intent string, u unit.Unit, guidance string, inputs map[string]any) (map[string]any, error)
}

// Guides are the profile's guides an intent's instructions compose.
type Guides interface {
	// Ready refuses a turn of d whose instructions compose guides that do
	// not read.
	Ready(d Def) error
}

// KeptGuidance is the user's guidance kept for a unit's next turn.
type KeptGuidance interface {
	Take(ctx context.Context, u unit.Unit) (string, error)
	// Keep stores guidance a turn took and did not use, unless newer guidance is stored.
	Keep(ctx context.Context, u unit.Unit, guidance string) error
}

// Loop is the loop's mutex: Enter reads ahead what u's turn reads, takes the
// mutex and returns the context to run under and its release.
type Loop interface {
	Enter(ctx context.Context, u unit.Unit) (context.Context, func(), error)
}

// TurnOut is what a turn's effects read of a turn the runner recorded.
type TurnOut struct {
	Def      Def
	Unit     unit.Unit
	Route    string
	Reason   string
	Guidance string
	Inputs   map[string]any
	Outcome  Outcome
	// Planned is a turn that sends a round its unit's plan named.
	Planned bool
}

// TurnEffects apply the turn effects an intent names, each at its phase.
type TurnEffects interface {
	// Check refuses a turn of d on u whose plugin effects no running plugin applies.
	Check(ctx context.Context, d Def, u unit.Unit) error
	// Before applies the host's effects that run before a person's turn goes out.
	Before(ctx context.Context, d Def, u unit.Unit) error
	// Recorded applies the plugin's effects of a recorded turn.
	Recorded(ctx context.Context, o TurnOut) error
	// Sent applies the host's effects of a person's turn that went out.
	Sent(ctx context.Context, o TurnOut) error
	// Handed records the guidance a turn carried.
	Handed(ctx context.Context, o TurnOut) error
}

// Outcome is what a runner did with a turn: the id it recorded the turn
// under, the head it saw and whether the prompt reached the agent.
type Outcome struct {
	ID, Head  string
	Submitted bool
}

// Runner renders, places, records and delivers a turn Start admitted. An
// Outcome with no ID is a turn the runner did not record; a runner that
// recorded the turn and then failed answers its ID with the error.
type Runner interface {
	Run(ctx context.Context, t Turn) (Outcome, error)
}

// Guarder is a Runner that can refuse a turn before any effect applies.
type Guarder interface {
	Guard(ctx context.Context, t Turn) error
}

// KeptBack is a Runner told of the guidance Start kept back for the unit's
// next turn, so the person who asked hears of it; it is not told when the
// guidance the request gave was dropped.
type KeptBack interface {
	KeptBack(guidance string)
}

// Sessionless is a Runner whose turn runs in no agent session, such as a
// headless call: the guidance of its turn is recorded only when its intent
// hands guidance.
type Sessionless interface {
	Sessionless() bool
}

// RouteLaunch is the route of a turn the turn service or the headless
// runner starts, as the plugin protocol names it.
const RouteLaunch = "launch"

// Request is a new turn an entry asks for.
type Request struct {
	Unit     unit.Unit
	Intent   turn.Intent
	Guidance string
	Inputs   map[string]any
	Trigger  turn.Trigger
	// Route is how the turn goes out, as the plugin's turn effects read it.
	Route string
	// ByPerson is a turn a person asked for.
	ByPerson bool
	// Reason is how the person asked for it, as the host's sent effects
	// record it; "" for the route's own.
	Reason string
	// ResumeOf is the turn whose session the new turn resumes, 0 none.
	ResumeOf int64
	// Planned is a turn that sends a round its unit's plan named, whose
	// marks the plan writes once the round is sent.
	Planned bool
}

// Turn is a turn Start or Resume admitted; a sink records it once.
type Turn struct {
	ok       bool
	used     *atomic.Bool
	unit     unit.Unit
	intent   turn.Intent
	guidance string
	inputs   map[string]any
	trigger  turn.Trigger
	route    string
	byPerson bool
	reason   string
	resumeOf int64
	resumed  bool
	planned  bool
}

func (t Turn) Unit() unit.Unit        { return t.unit }
func (t Turn) Intent() turn.Intent    { return t.intent }
func (t Turn) Guidance() string       { return t.guidance }
func (t Turn) Inputs() map[string]any { return maps.Clone(t.inputs) }
func (t Turn) Trigger() turn.Trigger  { return t.trigger }
func (t Turn) ResumeOf() int64        { return t.resumeOf }

// Resumed is a turn Resume admitted, whose effects its first turn's start
// applied: Start applied none of its own.
func (t Turn) Resumed() bool { return t.resumed }

func admitted(t Turn) Turn {
	t.ok, t.used = true, new(atomic.Bool)
	return t
}

// identify is r with t's identity.
func (t Turn) identify(r SendRequest) SendRequest {
	r.Unit, r.Intent, r.Trigger, r.Guidance, r.ResumeOf = t.unit, t.intent, t.trigger, t.guidance, t.resumeOf
	return r
}

// Use claims t for the one record it allows.
func (t Turn) Use() error {
	if !t.ok {
		return ErrNoTurn
	}
	if !t.used.CompareAndSwap(false, true) {
		return ErrUsedTurn
	}
	return nil
}

// Starter is the one way a new turn starts: it applies the intent's role
// and effects around the runner, in one order whatever entry asks.
type Starter struct {
	Defs     Defs
	Preparer Preparer
	Kept     KeptGuidance
	Loop     Loop
	Effects  TurnEffects
	Store    Store
	// Guides check the profile's guides before anything is touched for a
	// turn; nil checks none.
	Guides Guides
	// Log reports what fails without failing the start; nil drops it.
	Log func(format string, args ...any)
}

// Start admits r's turn under the loop and runs it with x.
func (s *Starter) Start(ctx context.Context, r Request, x Runner) (Outcome, error) {
	d, ok := s.Defs.Def(r.Intent)
	if !ok {
		return Outcome{}, fmt.Errorf("no intent %s", r.Intent)
	}
	if !lock.Held(ctx) {
		lctx, release, err := s.Loop.Enter(ctx, r.Unit)
		if err != nil {
			return Outcome{}, err
		}
		defer release()
		ctx = lock.WithHeld(lctx)
	}
	whole, kept := r.Guidance, ""
	if d.HandsGuidance {
		var err error
		if kept, err = s.Kept.Take(ctx, r.Unit); err != nil {
			s.logf("%s: reading the kept guidance: %v", r.Unit, err)
		}
		if whole == "" {
			whole = kept
		}
	}
	t := admitted(Turn{unit: r.Unit, intent: r.Intent, guidance: cut(d, whole), inputs: maps.Clone(r.Inputs), trigger: cmp.Or(r.Trigger, turn.ByHand),
		route: r.Route, byPerson: r.ByPerson, reason: r.Reason, resumeOf: r.ResumeOf, planned: r.Planned})
	o, err := s.run(ctx, d, &t, x)
	back, carried := whole, true
	if errors.Is(err, ErrAnswered) {
		back, carried = kept, r.Guidance == ""
	}
	if d.HandsGuidance && back != "" && (err != nil || !o.Submitted) {
		if kerr := s.Kept.Keep(ctx, r.Unit, back); kerr != nil {
			s.logf("%s: keeping the guidance back: %v", r.Unit, kerr)
		} else if k, ok := x.(KeptBack); ok && carried {
			k.KeptBack(back)
		}
	}
	if err != nil {
		return Outcome{}, err
	}
	return o, nil
}

// run is Start past the take: guides, prepare, check, guard, before, run,
// then the effects of the recorded turn; a turn recorded and not run
// through still records the guidance it carried.
func (s *Starter) run(ctx context.Context, d Def, t *Turn, x Runner) (Outcome, error) {
	if s.Guides != nil {
		if err := s.Guides.Ready(d); err != nil {
			return Outcome{}, err
		}
	}
	if d.Prepare != "" {
		if s.Preparer == nil {
			return Outcome{}, fmt.Errorf("%s: no prepare runs %s", t.unit, d.Prepare)
		}
		filled, err := s.Preparer.PrepareTurn(ctx, string(t.intent), t.unit, t.guidance, maps.Clone(t.inputs))
		if err != nil {
			return Outcome{}, err
		}
		t.inputs = filled
	}
	if err := s.Effects.Check(ctx, d, t.unit); err != nil {
		return Outcome{}, err
	}
	if g, ok := x.(Guarder); ok {
		if err := g.Guard(ctx, *t); err != nil {
			return Outcome{}, err
		}
	}
	if t.byPerson {
		if err := s.Effects.Before(ctx, d, t.unit); err != nil {
			return Outcome{}, err
		}
	}
	o, err := x.Run(ctx, *t)
	out := TurnOut{Def: d, Unit: t.unit, Route: t.route, Reason: t.reason, Guidance: t.guidance, Inputs: maps.Clone(t.inputs), Outcome: o, Planned: t.planned}
	sl, ok := x.(Sessionless)
	recordsGuidance := d.HandsGuidance || !ok || !sl.Sessionless()
	handed := func() {
		if recordsGuidance && t.guidance != "" && o.ID != "" {
			s.effect(ctx, "recording the guidance handed", out, s.Effects.Handed)
		}
	}
	if err != nil {
		handed()
		return Outcome{}, err
	}
	s.effect(ctx, "applying the plugin's turn effects", out, s.Effects.Recorded)
	if t.byPerson && o.Submitted {
		s.effect(ctx, "applying the sent effects", out, s.Effects.Sent)
	}
	handed()
	return o, nil
}

// effect applies one phase of a recorded turn's effects; the turn is out
// whatever it answers.
func (s *Starter) effect(ctx context.Context, what string, o TurnOut, apply func(context.Context, TurnOut) error) {
	if err := apply(context.WithoutCancel(ctx), o); err != nil {
		s.logf("%s: %s: %v", o.Unit, what, err)
	}
}

// Resume admits the turn that resumes u's stored turn seq: its intent,
// trigger, guidance and inputs, with no guidance taken and no host effect.
func (s *Starter) Resume(ctx context.Context, u unit.Unit, seq int64) (Turn, error) {
	return resume(ctx, s.Store, u, seq)
}

// ResumeAnswered is Resume of a turn whose guidance is the answer said, as
// a headless turn, which reads no resume of its own, takes it.
func (s *Starter) ResumeAnswered(ctx context.Context, u unit.Unit, seq int64, said string) (Turn, error) {
	t, err := resume(ctx, s.Store, u, seq)
	if err != nil {
		return Turn{}, err
	}
	t.guidance = said
	return t, nil
}

func resume(ctx context.Context, store Store, u unit.Unit, seq int64) (Turn, error) {
	b, err := store.Books(ctx, u)
	if err != nil {
		return Turn{}, err
	}
	book := b[u]
	q, ok := bySeq(&book, seq)
	if !ok {
		return Turn{}, Gone{Reason: fmt.Sprintf("%s has no turn %d", u.Label(), seq)}
	}
	return admitted(Turn{unit: u, intent: q.Intent, guidance: q.Guidance, inputs: InputsOf(*q), trigger: cmp.Or(q.Trigger, turn.ByHand), resumeOf: seq, resumed: true}), nil
}

func (s *Starter) logf(format string, args ...any) {
	if s.Log != nil {
		s.Log(format, args...)
	}
}

// cut is g within d's guidance.max.
func cut(d Def, g string) string {
	if r := []rune(g); d.Guidance.Max > 0 && len(r) > d.Guidance.Max {
		return string(r[:d.Guidance.Max])
	}
	return g
}
