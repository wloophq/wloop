package sqlite

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestVersion15DropsTheDossierTablesAndKeepsTheirEvents(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	frozen, err := os.ReadFile(filepath.Join("testdata", "schema-v14.sql"))
	if err != nil {
		t.Fatal(err)
	}
	path := storeAt(t, schemaSet{14, string(frozen), nil})
	rawExec(t, path,
		`INSERT INTO dossiers (owner, rel_path, status) VALUES ('task/t-0000000000000001', 'task/t-0000000000000001#', 'active')`,
		`INSERT INTO dossier_moves (seq, from_path, to_path) VALUES (1, 'slack/C1/1#', 'task/t-0000000000000001#/merged/x')`,
		`INSERT INTO events (provider, unit_key, kind, key, actor, at, data) VALUES ('task', 't-0000000000000001', 'dossier_restored', 'turn 1:log/x', '', '2026-11-04T10:00:00Z', 'log/x: a symlink')`,
		`INSERT INTO events (provider, unit_key, kind, key, actor, at, data) VALUES ('task', 't-0000000000000001', 'dossier_call', 'k1', '', '2026-11-04T10:00:00Z', '0.12')`,
	)
	if _, err := current.migrate(ctx, path, migrateOpts(path)); err != nil {
		t.Fatal(err)
	}
	db, err := current.open(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var tables int
	if err := db.R().QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name IN ('dossiers', 'dossier_moves')`).Scan(&tables); err != nil || tables != 0 {
		t.Fatalf("%d dossier tables remain (%v)", tables, err)
	}
	var events int
	if err := db.R().QueryRowContext(ctx, `SELECT COUNT(*) FROM events WHERE kind IN ('dossier_restored', 'dossier_call')`).Scan(&events); err != nil || events != 2 {
		t.Fatalf("the dossier events are %d, want 2 (%v)", events, err)
	}
}
