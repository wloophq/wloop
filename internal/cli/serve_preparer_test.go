package cli

import (
	"context"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/domain/unit"
)

func TestTheServedStarterPreparesThroughTheDaemonItWasBuiltWith(t *testing.T) {
	t.Parallel()
	d := ServeForTest(t, TestOptions{})
	d.e.linkStates.Store(nil)
	_, err := d.e.Turns.Starter.Preparer.PrepareTurn(context.Background(), "nudge", unit.Unit{Provider: unit.GitHub, Key: "acme/api#7"}, "", nil)
	if err == nil || !strings.Contains(err.Error(), "the GitHub plugin does not run") {
		t.Fatalf("prepare = %v, want the daemon's answer for a nudge no running plugin prepares", err)
	}
}
