package inventorytest

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/wloophq/wloop/internal/adapter/store/inventory"
	"github.com/wloophq/wloop/internal/adapter/store/sqlite/sqlitetest"
)

func TestOfPublishesAStoreOnlyOnceItsTablesExist(t *testing.T) {
	t.Parallel()
	db, err := sqlitetest.Open(filepath.Join(t.TempDir(), "store.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	st := inventory.New(db)
	var second error
	onPublish.Store(st, func() { _, second = Of(st).ListPRs(context.Background(), false) })
	t.Cleanup(func() { onPublish.Delete(st) })
	Of(st)
	if second != nil {
		t.Fatalf("a second Of read before the tables existed: %v", second)
	}
}
