package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/wloophq/wloop/internal/adapter/store/sqlite/sqlitetest"
	"github.com/wloophq/wloop/internal/workspace/fake"
)

func TestStoreMigrateLeavesTheDossiersOnDisk(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, &fake.Manager{})
	te.noDaemon(t)
	sqlitetest.At(t, te.storePath(), 14,
		`INSERT INTO dossiers (owner, rel_path, status) VALUES ('task/t-0a1b2c3d4e5f6a7b', 'task/t-0a1b2c3d4e5f6a7b#', 'active')`)
	notes := filepath.Join(te.StateDir, "dossiers", "task", "t-0a1b2c3d4e5f6a7b#", "notes.md")
	if err := os.MkdirAll(filepath.Dir(notes), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(notes, []byte("Ana wants March first.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if code := te.runCLI("store", "migrate", "--yes"); code != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	if got, err := os.ReadFile(notes); err != nil || string(got) != "Ana wants March first.\n" {
		t.Fatalf("the dossier's notes after the migration: %q %v", got, err)
	}
}
