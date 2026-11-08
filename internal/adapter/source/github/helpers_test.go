package ghsource

import (
	"context"
	"flag"
	"path/filepath"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/adapter/source/github/ghsourcetest"
	"github.com/wloophq/wloop/internal/adapter/store/inventory"
	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/domain/card"
	"github.com/wloophq/wloop/internal/domain/items"
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

// itemsOf are the items of r, a row ghsourcetest derived.
func itemsOf(r api.Row) []items.Item {
	rep, _ := ghsourcetest.ReportOf(r)
	return ItemsOf(r, rep)
}

// cardOf is the card of r, a row ghsourcetest derived.
func cardOf(_ context.Context, r api.Row) (card.Card, error) {
	rep, ok := ghsourcetest.ReportOf(r)
	if !ok {
		return card.Card{}, errNoReport
	}
	return CardOf(r, rep)
}
