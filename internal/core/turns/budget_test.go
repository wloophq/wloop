package turns_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/core/turns"
)

type atClock time.Time

func (c atClock) Now() time.Time { return time.Time(c) }

func TestBudgetReservesAndSettles(t *testing.T) {
	ctx := context.Background()
	day := time.Date(2026, 3, 4, 10, 0, 0, 0, time.UTC)
	var asked []time.Time
	b := &turns.Budget{Caps: turns.BudgetCaps{PerCallUSD: 0.5, PerDayUSD: 1.0}, Clock: atClock(day),
		Spent: func(_ context.Context, d time.Time) (float64, error) { asked = append(asked, d); return 0.6, nil }}
	if err := b.Reserve(ctx); err != nil {
		t.Fatalf("first reserve: %v", err)
	}
	if b.Reserved() != 0.5 {
		t.Fatalf("reserved %v, want 0.5", b.Reserved())
	}
	if err := b.Reserve(ctx); !errors.Is(err, turns.ErrOverCap) {
		t.Fatalf("second reserve: %v, want ErrOverCap", err)
	}
	var recorded []float64
	record := func(_ context.Context, cost float64) error { recorded = append(recorded, cost); return nil }
	if err := b.Settle(ctx, nil, record); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(recorded, []float64{0.5}) || b.Reserved() != 0 {
		t.Fatalf("recorded %v, reserved %v; want the per-call cap recorded and nothing held", recorded, b.Reserved())
	}
	if !asked[0].Equal(day) {
		t.Fatalf("spent asked for %v, want the clock's day %v", asked[0], day)
	}
}

func TestBudgetRecordsTheReportedCost(t *testing.T) {
	ctx := context.Background()
	b := &turns.Budget{Caps: turns.BudgetCaps{PerCallUSD: 0.5}, Clock: atClock(time.Time{})}
	if err := b.Reserve(ctx); err != nil {
		t.Fatal(err)
	}
	cost := 0.12
	var recorded []float64
	err := b.Settle(ctx, &cost, func(_ context.Context, c float64) error { recorded = append(recorded, c); return errors.New("disk full") })
	if err == nil || !slices.Equal(recorded, []float64{0.12}) || b.Reserved() != 0 {
		t.Fatalf("err %v, recorded %v, reserved %v; want the record's error, 0.12 and nothing held", err, recorded, b.Reserved())
	}
}

func TestBudgetWithoutADailyCapAlwaysReserves(t *testing.T) {
	b := &turns.Budget{Caps: turns.BudgetCaps{PerCallUSD: 0.5}, Clock: atClock(time.Time{}),
		Spent: func(context.Context, time.Time) (float64, error) { return 100, nil }}
	for range 3 {
		if err := b.Reserve(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
}

func TestBudgetFailsWhenTheSpendCannotBeRead(t *testing.T) {
	b := &turns.Budget{Caps: turns.BudgetCaps{PerCallUSD: 0.5, PerDayUSD: 1}, Clock: atClock(time.Time{}),
		Spent: func(context.Context, time.Time) (float64, error) { return 0, errors.New("locked") }}
	if err := b.Reserve(context.Background()); err == nil || errors.Is(err, turns.ErrOverCap) || b.Reserved() != 0 {
		t.Fatalf("err %v, reserved %v", err, b.Reserved())
	}
}
