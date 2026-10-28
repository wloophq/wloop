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

func TestTwoConcurrentMutesWriteOnce(t *testing.T) {
	t.Parallel()
	db, err := Open(filepath.Join(t.TempDir(), "overrides.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	ctx := context.Background()
	unmuted := map[string]string{port.OverrideMuteUntilStateChange: "", port.OverrideMutedKind: "", port.OverrideMutedWaitingOn: ""}
	for n := range 50 {
		u := unit.Unit{Provider: "acme", Key: fmt.Sprint("t", n)}
		var wg sync.WaitGroup
		written := make([]bool, 2)
		for i, who := range []string{"ana", "bruno"} {
			wg.Go(func() {
				mute := map[string]string{port.OverrideMuteUntilStateChange: port.OverrideTrue, port.OverrideMutedKind: "waiting", port.OverrideMutedWaitingOn: who}
				ok, err := db.SetOverride(ctx, u, mute, unmuted)
				if err != nil {
					t.Error(err)
				}
				written[i] = ok
			})
		}
		wg.Wait()
		if written[0] == written[1] {
			t.Fatalf("%s: written %v, want exactly one mute to win", u.Key, written)
		}
		winner := map[bool]string{true: "ana", false: "bruno"}[written[0]]
		if o, err := db.Override(ctx, u); err != nil || o[port.OverrideMutedWaitingOn] != winner {
			t.Fatalf("%s: override %v %v, want the winner %s", u.Key, o, err, winner)
		}
	}
}
