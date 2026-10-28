package ghsource

import (
	"flag"
	"path/filepath"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/adapter/store/inventory"
	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
)

var update = flag.Bool("update", false, "rewrite the golden files")

var now = time.Date(2026, 10, 5, 10, 0, 0, 0, time.UTC)

// openStore is an inventory over a fresh store.
func openStore(t *testing.T) *inventory.Store {
	t.Helper()
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "inv.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return inventory.New(db)
}
