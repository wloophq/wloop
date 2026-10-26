package cli

import (
	"context"
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
)

// TestReplayAStoreCopy replays the default workflows over a copy of a real
// store, as the seeded replay does but with no scenario, and writes the
// counts of what they proposed and withdrew per day, workflow and purpose
// to REPLAY_OUT, never a text, a name or an id. It runs only when
// REPLAY_STORE names a copy it may migrate in place, from
// REPLAY_START (RFC 3339).
func TestReplayAStoreCopy(t *testing.T) {
	path := os.Getenv("REPLAY_STORE")
	if path == "" {
		t.Skip("REPLAY_STORE names no store copy to replay")
	}
	ctx := context.Background()
	start, err := time.Parse(time.RFC3339, os.Getenv("REPLAY_START"))
	if err != nil {
		t.Fatalf("REPLAY_START: %v", err)
	}
	if _, err := sqlite.Migrate(ctx, path, sqlite.MigrateOptions{Backups: t.TempDir(), Now: func() time.Time { return start }}); err != nil {
		t.Fatal(err)
	}
	db, err := sqlite.Open(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	r := newReplayOn(t, db, start.In(saoPaulo()))
	r.counts = map[string]int{}
	p := newWorkflowProposer(r)
	r.prime()
	r.run(p)
	var b strings.Builder
	for _, k := range slices.Sorted(maps.Keys(r.counts)) {
		fmt.Fprintf(&b, "%s %d\n", k, r.counts[k])
	}
	if err := os.WriteFile(os.Getenv("REPLAY_OUT"), []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
}
