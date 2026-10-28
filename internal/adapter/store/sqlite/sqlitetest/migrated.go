package sqlitetest

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
)

// Migrated creates a store at path from the frozen schema of a released
// version, runs seed on it with that version's columns, then migrates it
// with this release and opens it.
func Migrated(t testing.TB, path string, version uint32, seed ...string) *sqlite.DB {
	t.Helper()
	At(t, path, version, seed...)
	if _, err := sqlite.Migrate(context.Background(), path, sqlite.MigrateOptions{
		Backups: filepath.Join(filepath.Dir(path), "backups"),
		Now:     func() time.Time { return time.Date(2026, 11, 2, 21, 30, 4, 0, time.UTC) },
	}); err != nil {
		t.Fatal(err)
	}
	db, err := sqlite.Open(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// At creates a store at path from the frozen schema of a released version
// and runs seed on it with that version's columns, leaving it at that version.
func At(t testing.TB, path string, version uint32, seed ...string) {
	t.Helper()
	_, self, _, _ := runtime.Caller(0)
	frozen, err := os.ReadFile(filepath.Join(filepath.Dir(self), "..", "testdata", fmt.Sprintf("schema-v%d.sql", version)))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	raw.SetMaxOpenConns(1)
	stmts := append([]string{string(frozen),
		fmt.Sprintf(`INSERT INTO meta (key, value) VALUES ('compatible_version', '%d')`, version),
		fmt.Sprintf(`PRAGMA user_version = %d`, version)}, seed...)
	for _, q := range stmts {
		if _, err := raw.Exec(q); err != nil {
			raw.Close()
			t.Fatalf("%s: %v", q, err)
		}
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}
}
