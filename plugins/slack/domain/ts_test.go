package slack

import (
	"testing"
	"time"
)

func TestTSTime(t *testing.T) {
	for ts, want := range map[string]time.Time{
		"1700000000.000100": time.Unix(1700000000, 100_000).UTC(),
		"1700000000":        time.Unix(1700000000, 0).UTC(),
		"":                  {},
		"soon":              {},
	} {
		if got := TSTime(ts); !got.Equal(want) {
			t.Errorf("TSTime(%q) = %v, want %v", ts, got, want)
		}
	}
}
