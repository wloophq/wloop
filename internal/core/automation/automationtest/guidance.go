package automationtest

import (
	"context"
	"slices"
	"sync"
	"testing"

	"github.com/wloophq/wloop/internal/domain/unit"
)

// Guidance is the user's stored guidance per unit, in memory.
type Guidance struct {
	o options

	mu    sync.Mutex
	text  map[unit.Unit]string
	taken []unit.Unit
}

func NewGuidance(opts ...Option) *Guidance {
	return &Guidance{o: build(opts), text: map[unit.Unit]string{}}
}

func (f *Guidance) Set(u unit.Unit, text string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.text[u] = text
}

// Taken are the units whose guidance Take consumed, in order.
func (f *Guidance) Taken() []unit.Unit {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.taken)
}

func (f *Guidance) Peek(ctx context.Context, u unit.Unit) (string, error) {
	f.o.read("guidance.peek")
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.text[u], nil
}

func (f *Guidance) Take(ctx context.Context, u unit.Unit) (string, error) {
	f.o.write("guidance.take")
	f.mu.Lock()
	defer f.mu.Unlock()
	g := f.text[u]
	delete(f.text, u)
	f.taken = append(f.taken, u)
	return g, nil
}

// Strict makes the fake fail t on any later write.
func (f *Guidance) Strict(t testing.TB) { f.o.seal(t) }

func (f *Guidance) Keep(ctx context.Context, u unit.Unit, guidance string) error {
	f.o.write("guidance.keep")
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.text[u] == "" && guidance != "" {
		f.text[u] = guidance
	}
	return nil
}
