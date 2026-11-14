package turnstest

import (
	"context"
	"maps"
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

func (d Defs) ByRole(role string, p unit.ProviderID) (turns.Def, bool) {
	def, ok := d.Registry.ByRole(role, p)
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

// Effects are turn effects that apply nothing and count the Before phases
// they were asked for.
type Effects struct {
	noEffects
	mu      sync.Mutex
	befores int
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
