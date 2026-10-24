package claude

import (
	"context"
	"testing"

	"github.com/wloophq/wloop/internal/meter"
	"github.com/wloophq/wloop/internal/meter/metertest"
)

func TestAskIsOneAgentSpawn(t *testing.T) {
	ctx, sc := metertest.Start(t, context.Background())
	if _, err := Exec(ctx, t.TempDir(), nil, "", "/bin/echo", "hi"); err != nil {
		t.Fatal(err)
	}
	if _, err := Shell(ctx, t.TempDir(), nil, "", "/bin/echo", "hi"); err != nil {
		t.Fatal(err)
	}
	for _, op := range []string{"ask", "shell"} {
		if n := metertest.Count(sc, metertest.Limit{Res: meter.Agent, Op: op, Field: metertest.Spawns}); n != 1 {
			t.Errorf("agent %s spawns = %d, want 1", op, n)
		}
	}
	if n := metertest.Count(sc, metertest.Limit{Res: meter.Agent, Field: metertest.Errors}); n != 0 {
		t.Errorf("agent errors = %d, want 0", n)
	}
}
