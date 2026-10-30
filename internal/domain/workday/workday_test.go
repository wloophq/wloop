package workday

import (
	"testing"
	"time"
)

func TestBetweenIgnoresLocation(t *testing.T) {
	brt := time.FixedZone("BRT", -3*3600)
	from := time.Date(2026, 10, 2, 22, 0, 0, 0, brt) // Friday 22:00 BRT, Saturday 01:00 UTC
	to := time.Date(2026, 10, 5, 9, 0, 0, 0, brt)    // Monday 09:00 BRT, Monday 12:00 UTC
	if got, want := Between(from, to), Between(from.UTC(), to.UTC()); got != want || got != 12*time.Hour {
		t.Fatalf("got %v, UTC inputs %v, want 12h", got, want)
	}
	sat := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	mon := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	if got := Between(sat, mon); got != 12*time.Hour {
		t.Fatalf("saturday noon to monday noon: %v", got)
	}
}

func TestAddInvertsBetween(t *testing.T) {
	fri := time.Date(2026, 10, 2, 22, 0, 0, 0, time.UTC)
	sat := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	for _, c := range []struct {
		from time.Time
		d    time.Duration
		want time.Time
	}{
		{fri, 0, fri},
		{fri, time.Hour, fri.Add(time.Hour)},
		{fri, 2 * time.Hour, time.Date(2026, 10, 3, 0, 0, 0, 0, time.UTC)},
		{fri, 2*time.Hour + time.Nanosecond, time.Date(2026, 10, 5, 0, 0, 0, 1, time.UTC)},
		{fri, 3 * time.Hour, time.Date(2026, 10, 5, 1, 0, 0, 0, time.UTC)},
		{sat, 12 * time.Hour, time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)},
		{fri, 50 * time.Hour, time.Date(2026, 10, 7, 0, 0, 0, 0, time.UTC)},
	} {
		got := Add(c.from, c.d)
		if !got.Equal(c.want) {
			t.Errorf("Add(%v, %v) = %v, want %v", c.from, c.d, got, c.want)
		}
		if b := Between(c.from, got); b != c.d {
			t.Errorf("Between(%v, Add) = %v, want %v", c.from, b, c.d)
		}
		if c.d > 0 && Between(c.from, got.Add(-time.Nanosecond)) >= c.d {
			t.Errorf("Add(%v, %v) is not the earliest instant", c.from, c.d)
		}
	}
}
