package turnstest

import (
	"context"
	"maps"
	"slices"
	"sync"

	"github.com/wloophq/wloop/internal/core/turns"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	"github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
)

// Starter is a turns.Starter over defs and store whose loop, kept guidance
// and effects do nothing; p runs the prepares, nil none.
func Starter(defs Defs, store turns.Store, p turns.Preparer) *turns.Starter {
	if p == nil {
		p = &Prepares{}
	}
	return &turns.Starter{Defs: defs, Preparer: p, Kept: noKept{}, Loop: noLoop{}, Effects: noEffects{}, Store: store}
}

// Defs are the intents of a registry as Start reads them, each changed by
// Edit when set.
type Defs struct {
	Registry *dintent.Registry
	Edit     func(d *turns.Def)
}

func (d Defs) Def(name turn.Intent) (turns.Def, bool) {
	def, ok := d.Registry.Get(dintent.Name(name))
	return d.edited(def, ok)
}

func (d Defs) edited(def dintent.Def, ok bool) (turns.Def, bool) {
	out := turns.Def{Def: def}
	if ok && d.Edit != nil {
		d.Edit(&out)
	}
	return out, ok
}

// Prepares is a turns.Preparer that fills Filled, or refuses with Refusal,
// keeping the guidance each prepare saw.
type Prepares struct {
	Filled  map[string]any
	Refusal error

	mu  sync.Mutex
	saw []string
}

func (p *Prepares) PrepareTurn(_ context.Context, _ string, _ unit.Unit, guidance string, inputs map[string]any) (map[string]any, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.saw = append(p.saw, guidance)
	if p.Refusal != nil {
		return nil, p.Refusal
	}
	out := maps.Clone(inputs)
	if out == nil {
		out = map[string]any{}
	}
	maps.Copy(out, p.Filled)
	return out, nil
}

// Saw is the guidance of each prepare run, in order.
func (p *Prepares) Saw() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.saw...)
}

type noKept struct{}

func (noKept) Take(context.Context, unit.Unit) (string, error) { return "", nil }
func (noKept) Keep(context.Context, unit.Unit, string) error   { return nil }

type noLoop struct{}

func (noLoop) Enter(ctx context.Context, _ unit.Unit) (context.Context, func(), error) {
	return ctx, func() {}, nil
}

type noEffects struct{}

func (noEffects) Check(context.Context, turns.Def, unit.Unit) error  { return nil }
func (noEffects) Before(context.Context, turns.Def, unit.Unit) error { return nil }
func (noEffects) Recorded(context.Context, turns.TurnOut) error      { return nil }
func (noEffects) Sent(context.Context, turns.TurnOut) error          { return nil }
func (noEffects) Handed(context.Context, turns.TurnOut) error        { return nil }

// Effects are turn effects that apply nothing, counting the Before phases
// they were asked for and keeping the guidance each Handed recorded.
type Effects struct {
	noEffects
	mu      sync.Mutex
	befores int
	handed  []string
}

func (e *Effects) Handed(_ context.Context, o turns.TurnOut) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.handed = append(e.handed, o.Guidance)
	return nil
}

// Guidances are the guidance each recorded turn's Handed recorded, in order.
func (e *Effects) Guidances() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return slices.Clone(e.handed)
}

func (e *Effects) Before(context.Context, turns.Def, unit.Unit) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.befores++
	return nil
}

// Befores is how many turns ran their Before phase.
func (e *Effects) Befores() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.befores
}

// Started is the turn a start of r admits, for a test that hands a sink a
// turn of its own: no prepare runs and no guidance is taken.
func Started(r turns.Request) turns.Turn {
	var got turns.Turn
	s := &turns.Starter{Defs: anyDefs{}, Preparer: &Prepares{}, Kept: noKept{}, Loop: noLoop{}, Effects: noEffects{}}
	if _, err := s.Start(context.Background(), r, admit{&got}); err != nil {
		panic(err)
	}
	return got
}

// anyDefs know every intent, with no role.
type anyDefs struct{}

func (anyDefs) Def(name turn.Intent) (turns.Def, bool) {
	return turns.Def{Def: dintent.Def{Name: dintent.Name(name)}}, true
}

// admit is a runner that keeps the turn it is given, unused.
type admit struct{ got *turns.Turn }

func (a admit) Run(_ context.Context, t turns.Turn) (turns.Outcome, error) {
	*a.got = t
	return turns.Outcome{}, nil
}

// Sent is the turn admitted with the identity r carries.
func Sent(r turns.SendRequest) turns.Turn {
	return Started(turns.Request{Unit: r.Unit, Intent: r.Intent, Guidance: r.Guidance, Inputs: r.Inputs, Trigger: r.Trigger, ResumeOf: r.ResumeOf})
}

// Send is s.Send of r, as a turn admitted with r's identity.
func Send(ctx context.Context, s *turns.Service, r turns.SendRequest) (turn.Turn, error) {
	return s.Send(ctx, Sent(r), r)
}

// StartByHand is s.StartByHand of r, as a turn admitted with r's identity.
func StartByHand(ctx context.Context, s *turns.Service, r turns.SendRequest, handle string) (turn.Turn, error) {
	return s.StartByHand(ctx, Sent(r), r, handle)
}

// BeginHeadless is s.BeginHeadless of h, as a turn admitted with h's identity.
func BeginHeadless(ctx context.Context, s *turns.Service, h turns.HeadlessStart) (int64, error) {
	x := Started(turns.Request{Unit: h.Unit, Intent: h.Intent, Guidance: h.Guidance, Inputs: h.Inputs, Trigger: h.Trigger, ResumeOf: h.ResumeOf})
	return s.BeginHeadless(ctx, x, h)
}
