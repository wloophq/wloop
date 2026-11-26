package cli

import (
	"context"
	"path/filepath"
	"sync"
	"testing"

	"github.com/wloophq/wloop/internal/adapter/store/sqlite/sqlitetest"
	"github.com/wloophq/wloop/internal/core/change"
)

// TestTheFactsAndTheDaemonShareOneGatesService builds the gates service from several goroutines while the task
// facts read it, as serve's wirings and a sweep do: under -race, no access races, and every builder gets the
// one service the facts read.
func TestTheFactsAndTheDaemonShareOneGatesService(t *testing.T) {
	te := newTestEnv(t, nil)
	db, err := sqlitetest.Open(filepath.Join(t.TempDir(), "s.db"), change.NewLog(change.DefaultWindow, nil))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	ctx := context.Background()
	var wg sync.WaitGroup
	built := make([]any, 8)
	for i := range built {
		wg.Go(func() {
			if i%2 == 0 {
				built[i] = te.newGates(db, nil)
				return
			}
			if _, err := te.pinnedDrafts(ctx); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	svc, _ := te.builtGates()
	for i := 0; i < len(built); i += 2 {
		if built[i] != any(svc) {
			t.Fatalf("builder %d got another gates service than the facts read", i)
		}
	}
}
