// Package sqlitetest seeds a store with raw SQL in tests.
package sqlitetest

import (
	"context"
	"database/sql"
	"testing"

	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
)

// Exec runs one statement in its own write transaction and fails t on error.
// It notes nothing.
func Exec(t testing.TB, db *sqlite.DB, query string, args ...any) sql.Result {
	t.Helper()
	var res sql.Result
	_, err := db.Write(context.Background(), func(tx *sqlite.Tx) error {
		var err error
		res, err = tx.ExecContext(context.Background(), query, args...)
		return err
	})
	if err != nil {
		t.Fatalf("exec %q: %v", query, err)
	}
	return res
}
