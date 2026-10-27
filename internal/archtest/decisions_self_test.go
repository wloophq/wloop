package archtest

import (
	"testing"

	"github.com/wloophq/wloop/internal/archtest/decisionlog"
)

func TestDecisionTestsFlagsAnEntryWithNoTestOrAMissingOne(t *testing.T) {
	t.Parallel()
	ds := []decisionlog.Decision{
		{Slug: "kept", Tests: []string{"TestKept"}},
		{Slug: "untested"},
		{Slug: "stale", Tests: []string{"TestKept", "TestGone"}},
	}
	flagged(t, "planted", decisionTests(ds, map[string]bool{"TestKept": true}), 2, "decisions/")
}
