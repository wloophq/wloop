package sqlitetest

import (
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"
)

// WriteLockHeld says whether a second connection to the database at path,
// with no busy timeout, is refused a write transaction.
func WriteLockHeld(t *testing.T, path string) bool {
	t.Helper()
	other, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(0)")
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	if _, err := other.Exec(`BEGIN IMMEDIATE`); err != nil {
		return true
	}
	other.Exec(`ROLLBACK`)
	return false
}
