package event

import (
	"testing"
	"time"
)

func TestRequestKeyIsTheReviewerAndTheSecondInUTC(t *testing.T) {
	at := time.Date(2026, 10, 16, 6, 0, 0, 500, time.FixedZone("BRT", -3*3600))
	if got := RequestKey("team:acme/search", at); got != "team:acme/search:2026-10-16T09:00:00Z" {
		t.Fatalf("key %q", got)
	}
}
