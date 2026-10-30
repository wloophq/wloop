package sqlite

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"testing"

	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/domain/unit"
)

func TestTwoConcurrentGuidancesWriteOnce(t *testing.T) {
	t.Parallel()
	db, err := Open(filepath.Join(t.TempDir(), "overrides.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	ctx := context.Background()
	unset := map[string]string{port.OverrideNextGuidance: ""}
	for n := range 50 {
		u := unit.Unit{Provider: "acme", Key: fmt.Sprint("t", n)}
		var wg sync.WaitGroup
		written := make([]bool, 2)
		for i, who := range []string{"ana", "bruno"} {
			wg.Go(func() {
				ok, err := db.SetOverride(ctx, u, map[string]string{port.OverrideNextGuidance: "ask " + who}, unset)
				if err != nil {
					t.Error(err)
				}
				written[i] = ok
			})
		}
		wg.Wait()
		if written[0] == written[1] {
			t.Fatalf("%s: written %v, want exactly one guidance to win", u.Key, written)
		}
		winner := map[bool]string{true: "ana", false: "bruno"}[written[0]]
		if o, err := db.Override(ctx, u); err != nil || o[port.OverrideNextGuidance] != "ask "+winner {
			t.Fatalf("%s: override %v %v, want the winner %s", u.Key, o, err, winner)
		}
	}
}
