package cli

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	"github.com/wloophq/wloop/internal/adapter/store/sqlite/sqlitetest"
	"github.com/wloophq/wloop/internal/core/headless"
	"github.com/wloophq/wloop/internal/core/lock"
	"github.com/wloophq/wloop/internal/core/omnibox"
	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/workspace/fake"
)

// servedStore serves the daemon's deps on a fresh store.
func servedStore(t *testing.T) (*testEnv, *sqlite.DB) {
	t.Helper()
	te := newTestEnv(t, &fake.Manager{})
	te.cfg = withoutWindows(te.cfg)
	db, err := sqlitetest.Open(filepath.Join(t.TempDir(), storeFile), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	te.serveDeps(ctx, db, te.cfg, t.Logf, &lock.Mutex{}, nil)
	if te.memory == nil {
		t.Fatal("the memory is not wired into the turns")
	}
	return te, db
}

func serveDepsAgain(t *testing.T, te *testEnv, db *sqlite.DB) *headless.Service {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	svc, ok := te.serveDeps(ctx, db, te.cfg, t.Logf, &lock.Mutex{}, nil).Headless.(*headless.Service)
	if !ok {
		t.Fatal("no headless service")
	}
	return svc
}

func TestHeadlessCapCountsOmniboxCalls(t *testing.T) {
	t.Parallel()
	te, db := servedStore(t)
	ctx := context.Background()
	if err := sqlite.New(db).RecordEvents(ctx, port.Event{Kind: omnibox.KindCall, Key: "c1", At: testNow, Data: "0.9"}); err != nil {
		t.Fatal(err)
	}
	te.cfg.Headless.PerCallUSD, te.cfg.Headless.PerDayUSD = 0.2, 1.0
	d := serveDepsAgain(t, te, db)
	if spent, err := d.Spent(ctx, testNow); err != nil || spent != 0.9 {
		t.Fatalf("the headless cap reads %v spent: %v", spent, err)
	}
	if err := te.callBudget.Reserve(ctx); err != nil {
		t.Fatal(err)
	}
	if spent, err := d.Spent(ctx, testNow); err != nil || spent != 0.9+0.2 {
		t.Fatalf("with a call running the headless cap reads %v spent: %v", spent, err)
	}
	if err := te.callBudget.Reserve(ctx); !errors.Is(err, headless.ErrOverCap) {
		t.Fatalf("a second call past the day's cap: %v", err)
	}
}
