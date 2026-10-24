package turnstest

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/core/turns"
	"github.com/wloophq/wloop/internal/domain/turn"
)

// Prompts is an in-memory turns.Prompts keyed by handle: a handle Put never
// named has no readable transcript.
type Prompts struct {
	mu   sync.Mutex
	last map[string]time.Time
	err  error
}

var _ turns.Prompts = (*Prompts)(nil)

func NewPrompts() *Prompts { return &Prompts{last: map[string]time.Time{}} }

// Put makes the handle's transcript readable, its last prompt at at; a zero
// at is a transcript with no prompt.
func (p *Prompts) Put(handle string, at time.Time) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.last[handle] = at
}

// Fail makes every read return err.
func (p *Prompts) Fail(err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.err = err
}

func (p *Prompts) LastUserPrompt(_ context.Context, sess turn.Session) (time.Time, bool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.err != nil {
		return time.Time{}, false, p.err
	}
	at, ok := p.last[sess.ID]
	return at, ok, nil
}

// PromptsWorld is what an implementation under RunPrompts holds: Prompted's
// transcript was last prompted at At, Quiet's has no prompt, and Unknown has
// none that can be found.
type PromptsWorld struct {
	Prompted, Quiet, Unknown turn.Session
	At                       time.Time
}

// RunPrompts is the contract every turns.Prompts passes.
func RunPrompts(t *testing.T, mk func(t *testing.T) (turns.Prompts, PromptsWorld)) {
	ctx := context.Background()
	t.Run("the last prompt of a readable transcript", func(t *testing.T) {
		p, w := mk(t)
		at, ok, err := p.LastUserPrompt(ctx, w.Prompted)
		if err != nil || !ok || !at.Equal(w.At) {
			t.Fatalf("LastUserPrompt = %v, %v, %v; want %v", at, ok, err, w.At)
		}
	})
	t.Run("a transcript with no prompt", func(t *testing.T) {
		p, w := mk(t)
		at, ok, err := p.LastUserPrompt(ctx, w.Quiet)
		if err != nil || !ok || !at.IsZero() {
			t.Fatalf("LastUserPrompt = %v, %v, %v; want a zero time, ok", at, ok, err)
		}
	})
	t.Run("no transcript", func(t *testing.T) {
		p, w := mk(t)
		if _, ok, err := p.LastUserPrompt(ctx, w.Unknown); err != nil || ok {
			t.Fatalf("LastUserPrompt = %v, %v; want not ok", ok, err)
		}
	})
}
