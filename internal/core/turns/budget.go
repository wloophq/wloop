package turns

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/wloophq/wloop/internal/core/port"
)

// ErrOverCap is a call the day's headless spend leaves no room for.
var ErrOverCap = errors.New("the headless spend cap is reached")

// BudgetCaps are the headless caps a Budget books against; a zero PerDayUSD caps no day.
type BudgetCaps struct {
	PerCallUSD, PerDayUSD float64
}

// Budget books the headless spend of calls that are not turns (the dossiers' calls, the omnibox's): each
// call reserves its per-call cap before it runs and settles its cost when it returns.
type Budget struct {
	Caps BudgetCaps
	// Spent is the day's headless spend as stored; nil counts none.
	Spent func(ctx context.Context, day time.Time) (float64, error)
	Clock port.Clock

	mu       sync.Mutex
	reserved float64
}

// Reserve books a call's cap, refusing it with ErrOverCap when the day's spend and the running calls
// reach PerDayUSD.
func (b *Budget) Reserve(ctx context.Context) error {
	var stored float64
	if b.Spent != nil {
		s, err := b.Spent(ctx, b.Clock.Now())
		if err != nil {
			return fmt.Errorf("reading the day's headless spend: %w", err)
		}
		stored = s
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.Caps.PerDayUSD > 0 && stored+b.reserved >= b.Caps.PerDayUSD {
		return fmt.Errorf("%w: $%.2f of $%.2f today", ErrOverCap, stored+b.reserved, b.Caps.PerDayUSD)
	}
	b.reserved += b.Caps.PerCallUSD
	return nil
}

// Settle records a reserved call's cost, the per-call cap when none was reported, and then releases its
// reservation, so the call always counts, stored or reserved. It returns record's error.
func (b *Budget) Settle(ctx context.Context, cost *float64, record func(ctx context.Context, cost float64) error) error {
	paid := b.Caps.PerCallUSD
	if cost != nil {
		paid = *cost
	}
	var err error
	if record != nil {
		err = record(context.WithoutCancel(ctx), paid)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.reserved -= b.Caps.PerCallUSD
	return err
}

// Reserved is the per-call caps of the calls running, which no store holds yet.
func (b *Budget) Reserved() float64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.reserved
}
