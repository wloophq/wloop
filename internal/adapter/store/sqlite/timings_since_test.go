package sqlite

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/domain/event"
)

func TestAFreshStoreRecordsWhenTimingsBegin(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "inv.db")
	before := time.Now().Add(-time.Second)
	db, err := Open(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	var raw string
	if err := db.R().QueryRowContext(context.Background(), `SELECT value FROM meta WHERE key = 'timings_since'`).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	at, err := time.Parse(event.TimeLayout, raw)
	if err != nil || at.Before(before) || at.After(time.Now()) {
		t.Fatalf("timings_since %q %v", raw, err)
	}
	db.Close()
	db, err = Open(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var again string
	if err := db.R().QueryRowContext(context.Background(), `SELECT value FROM meta WHERE key = 'timings_since'`).Scan(&again); err != nil || again != raw {
		t.Fatalf("reopened: %q %v", again, err)
	}
}
