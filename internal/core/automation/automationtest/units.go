package automationtest

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/core/automation"
	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/workflow"
)

// Units is an in-memory facts provider for every provider, with all the
// optional capabilities: Refresher, Settler, Gainer, Owner, Freshener, Seener, Dueer.
type Units struct {
	o options

	mu        sync.Mutex
	facts     map[unit.Unit]workflow.Facts
	errs      map[unit.Unit]error
	listErr   map[unit.ProviderID]error
	gains     map[unit.Unit]workflow.Gain
	owners    map[unit.Unit][]unit.Unit
	ownerErrs map[unit.Unit]error
	moved     map[unit.Unit]bool
	due       time.Time
	seen      port.Generation
	onRefresh func(context.Context) error
	onSettle  func(context.Context, automation.State) error
	gainAsked []unit.Unit
}

func NewUnits(opts ...Option) *Units {
	return &Units{o: build(opts), facts: map[unit.Unit]workflow.Facts{}, errs: map[unit.Unit]error{},
		listErr: map[unit.ProviderID]error{}, gains: map[unit.Unit]workflow.Gain{},
		owners: map[unit.Unit][]unit.Unit{}, ownerErrs: map[unit.Unit]error{}, moved: map[unit.Unit]bool{}}
}

// Put sets u's facts, listing u under its provider.
func (f *Units) Put(u unit.Unit, facts workflow.Facts) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.facts[u] = maps.Clone(facts)
}

// Set changes one fact of u.
func (f *Units) Set(u unit.Unit, name string, v any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.facts[u] == nil {
		f.facts[u] = workflow.Facts{}
	}
	f.facts[u][name] = v
}

// Remove stops listing u.
func (f *Units) Remove(u unit.Unit) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.facts, u)
}

// FailFacts makes Facts of u fail with err; nil clears it.
func (f *Units) FailFacts(u unit.Unit, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.errs[u] = err
}

// FailList makes List of p fail with err; nil clears it.
func (f *Units) FailList(p unit.ProviderID, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.listErr[p] = err
}

func (f *Units) OnRefresh(fn func(context.Context) error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.onRefresh = fn
}

func (f *Units) OnSettle(fn func(context.Context, automation.State) error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.onSettle = fn
}

func (f *Units) SetGain(u unit.Unit, g workflow.Gain) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.gains[u] = g
}

// SetOwners makes Owners of u answer owners; none clears them.
func (f *Units) SetOwners(u unit.Unit, owners ...unit.Unit) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.owners[u] = slices.Clone(owners)
}

// FailOwners makes Owners of u fail with err; nil clears it.
func (f *Units) FailOwners(u unit.Unit, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ownerErrs[u] = err
}

// SetMoved makes Moved report us, until the next SetMoved.
func (f *Units) SetMoved(us ...unit.Unit) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.moved = map[unit.Unit]bool{}
	for _, u := range us {
		f.moved[u] = true
	}
}

func (f *Units) SetDue(t time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.due = t
}

func (f *Units) SetSeen(g port.Generation) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.seen = g
}

// GainAsked are the units Gain was asked about, in order.
func (f *Units) GainAsked() []unit.Unit {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.gainAsked)
}

func (f *Units) List(ctx context.Context, p unit.ProviderID) ([]unit.Unit, error) {
	f.o.read("units.list")
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.listErr[p]; err != nil {
		return nil, err
	}
	var out []unit.Unit
	for u := range f.facts {
		if u.Provider == p {
			out = append(out, u)
		}
	}
	slices.SortFunc(out, compareUnits)
	return out, nil
}

func (f *Units) Facts(ctx context.Context, u unit.Unit) (workflow.Facts, error) {
	f.o.read("units.facts")
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.errs[u]; err != nil {
		return nil, err
	}
	facts, ok := f.facts[u]
	if !ok {
		return nil, fmt.Errorf("%s: %w", u, automation.ErrUnitGone)
	}
	return maps.Clone(facts), nil
}

func (f *Units) Refresh(ctx context.Context) error {
	f.o.write("units.refresh")
	f.mu.Lock()
	fn := f.onRefresh
	f.mu.Unlock()
	if fn != nil {
		return fn(ctx)
	}
	return nil
}

func (f *Units) Settle(ctx context.Context, st automation.State) error {
	f.o.write("units.settle")
	f.mu.Lock()
	fn := f.onSettle
	f.mu.Unlock()
	if fn != nil {
		return fn(ctx, st)
	}
	return nil
}

func (f *Units) Gain(ctx context.Context, u unit.Unit) (workflow.Gain, error) {
	f.o.read("units.gain")
	f.mu.Lock()
	defer f.mu.Unlock()
	f.gainAsked = append(f.gainAsked, u)
	return f.gains[u], nil
}

func (f *Units) Owners(ctx context.Context, u unit.Unit) ([]unit.Unit, error) {
	f.o.read("units.owners")
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.ownerErrs[u]; err != nil {
		return nil, err
	}
	return slices.Clone(f.owners[u]), nil
}

// Moved is a read, but the engine asks it only in Sweep; a strict fake counts it.
func (f *Units) Moved(ctx context.Context, units []unit.Unit) ([]unit.Unit, error) {
	f.o.write("units.moved")
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []unit.Unit
	for _, u := range units {
		if f.moved[u] {
			out = append(out, u)
		}
	}
	return out, nil
}

func (f *Units) Due() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.due
}

func (f *Units) Seen() port.Generation {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.seen
}

// Strict makes the fake fail t on any later write.
func (f *Units) Strict(t testing.TB) { f.o.seal(t) }
