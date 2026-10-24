package automationtest

import (
	"context"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/domain/workflow"
)

// Events is an in-memory events table with per-workflow cursors.
type Events struct {
	o options

	mu         sync.Mutex
	evs        []workflow.Event
	cursors    map[string]cursor
	advanceErr error
}

type cursor struct {
	at       int64
	lastFire time.Time
}

func NewEvents(opts ...Option) *Events {
	return &Events{o: build(opts), cursors: map[string]cursor{}}
}

// Append stores evs with the next ids and returns them as stored.
func (f *Events) Append(evs ...workflow.Event) []workflow.Event {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]workflow.Event, 0, len(evs))
	for _, ev := range evs {
		ev.ID = int64(len(f.evs)) + 1
		f.evs = append(f.evs, ev)
		out = append(out, ev)
	}
	return out
}

// FailAdvance makes Advance fail with err; nil clears it.
func (f *Events) FailAdvance(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.advanceErr = err
}

func (f *Events) Since(ctx context.Context, from int64, kinds []string) ([]workflow.Event, error) {
	f.o.read("events.since")
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []workflow.Event
	for _, ev := range f.evs {
		if ev.ID > from && (len(kinds) == 0 || slices.Contains(kinds, ev.Kind)) {
			out = append(out, ev)
		}
	}
	return out, nil
}

func (f *Events) Head(ctx context.Context) (int64, error) {
	f.o.read("events.head")
	f.mu.Lock()
	defer f.mu.Unlock()
	return int64(len(f.evs)), nil
}

func (f *Events) Cursor(ctx context.Context, wf string) (int64, time.Time, bool, error) {
	f.o.read("events.cursor")
	f.mu.Lock()
	defer f.mu.Unlock()
	c, ok := f.cursors[wf]
	return c.at, c.lastFire, ok, nil
}

func (f *Events) Advance(ctx context.Context, wf string, at int64, lastFire time.Time) error {
	f.o.write("events.advance")
	if err := ctx.Err(); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.advanceErr != nil {
		return f.advanceErr
	}
	f.cursors[wf] = cursor{at: at, lastFire: lastFire}
	return nil
}

// Strict makes the fake fail t on any later write.
func (f *Events) Strict(t testing.TB) { f.o.seal(t) }
