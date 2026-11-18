package sqlite

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// v7Set takes a store to version 7, the last whose migrations write the dossiers tables version 15 drops.
func v7Set(t *testing.T) schemaSet {
	t.Helper()
	frozen, err := os.ReadFile(filepath.Join("testdata", "schema-v7.sql"))
	if err != nil {
		t.Fatal(err)
	}
	return schemaSet{7, string(frozen), migrations[:6]}
}

// migrateToV7 migrates the store at path to version 7 and opens it raw.
func migrateToV7(t *testing.T, path string) *sql.DB {
	t.Helper()
	set := v7Set(t)
	if _, err := set.migrate(context.Background(), path, migrateOpts(path)); err != nil {
		t.Fatal(err)
	}
	return rawDB(t, path)
}

func TestV7RenameGivesTheTasksDossierItsNewOwner(t *testing.T) {
	t.Parallel()
	db := migrateToV7(t, v6Fixture(t))
	if got := strings1(t, db, `SELECT owner || ' ' || rel_path FROM dossiers`); !slices.Equal(got, []string{"task/t-" + hexA + " task/t-" + hexA + "#"}) {
		t.Errorf("dossiers %q", got)
	}
}

func TestV7KindsGivesTheDossierToTheOldID(t *testing.T) {
	t.Parallel()
	db := migrateToV7(t, v6KindsFixture(t))
	if got := strings1(t, db, `SELECT count(*) FROM dossiers WHERE owner = 'task/' || ?`, string(waitID)); !slices.Equal(got, []string{"1"}) {
		t.Errorf("dossiers of %s: %q", waitID, got)
	}
}
