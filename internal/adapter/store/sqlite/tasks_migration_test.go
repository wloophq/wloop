package sqlite

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestVersion8TakesATasksLastWriteAsWhenItOpenedOrClosed(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	frozen, err := os.ReadFile(filepath.Join("testdata", "schema-v7.sql"))
	if err != nil {
		t.Fatal(err)
	}
	path := storeAt(t, schemaSet{7, string(frozen), nil})
	rawExec(t, path,
		`INSERT INTO tasks (id, state, category, origin, created, updated, rev) VALUES ('t-0000000000000001', 'done', 'done', 'derived', '2026-10-01T10:00:00.000000000Z', '2026-10-02T10:00:00.000000000Z', 1)`,
	)
	if _, err := current.migrate(ctx, path, migrateOpts(path)); err != nil {
		t.Fatal(err)
	}
	db, err := current.open(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	e, err := db.Tasks().Task(ctx, "t-0000000000000001")
	if err != nil {
		t.Fatal(err)
	}
	if want := time.Date(2026, 10, 2, 10, 0, 0, 0, time.UTC); !e.OpenChanged.Equal(want) {
		t.Errorf("opened or closed at %v, want its last write %v", e.OpenChanged, want)
	}
}
