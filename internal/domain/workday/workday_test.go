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
