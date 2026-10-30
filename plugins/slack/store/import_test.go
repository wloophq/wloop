package store

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wloophq/wloop/pkg/plugin/store"
	"github.com/wloophq/wloop/plugins/slack/unit"
)

// exportedSchema is the store's schema as the host's core schema held the
// Slack tables, which the export copies: extraction_cursors has no brief.
func exportedSchema() string {
	return strings.Replace(schema, "  -- the JSON of the brief the extraction last wrote of the unit\n  brief TEXT NOT NULL DEFAULT '',\n", "", 1)
}

func plantImport(t *testing.T, dir string) {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(dir, store.ImportFile))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, q := range []string{
		exportedSchema(),
		`CREATE TABLE extraction_briefs (provider TEXT NOT NULL, unit_key TEXT NOT NULL, brief TEXT NOT NULL)`,
		`INSERT INTO slack_conversations (id, team, name, kind) VALUES ('C000AAA', 'T000AAA', 'general', 'channel')`,
		`INSERT INTO slack_messages (conversation, ts, user, text) VALUES ('C000AAA', '1700000000.000100', 'U000000ANA', 'can you look?')`,
		`INSERT INTO slack_objects (object, conversation, ts, kind, version) VALUES ('C000AAA/1700000000.000100', 'C000AAA', '1700000000.000100', 'thread', 'v1')`,
		`INSERT INTO extraction_cursors (provider, unit_key, last_ts, verdict, at) VALUES ('slack', 'T000AAA/C000AAA/1700000000.000100', '1700000000.000100', 'open', '2026-10-20T12:00:00Z')`,
		`INSERT INTO extraction_briefs (provider, unit_key, brief) VALUES ('slack', 'T000AAA/C000AAA/1700000000.000100', '{"goal":"review"}')`,
		`INSERT INTO slack_cursors (name, value, at) VALUES ('viewer_team', 'T000AAA', '2026-10-20T12:00:00Z')`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
}

func TestSlackImportsTheExportedStore(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dir := t.TempDir()
	plantImport(t, dir)
	db, err := Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	u := unit.Unit{Provider: "slack", Key: "T000AAA/C000AAA/1700000000.000100"}
	msgs, err := db.Slack().Messages(ctx, "C000AAA", MessageQuery{Thread: "1700000000.000100"})
	if err != nil || len(msgs) != 1 || msgs[0].Text != "can you look?" {
		t.Fatalf("imported messages %+v %v", msgs, err)
	}
	if team, err := db.Slack().Cursor(ctx, "viewer_team"); err != nil || team != "T000AAA" {
		t.Fatalf("imported cursor %q %v", team, err)
	}
	states, err := db.Extraction().States(ctx, []unit.Unit{u})
	if err != nil || states[u].LastTS != "1700000000.000100" || states[u].Verdict != "open" || states[u].Brief != `{"goal":"review"}` {
		t.Fatalf("imported state %+v %v", states[u], err)
	}
	if _, err := os.Stat(filepath.Join(dir, store.ImportFile)); !os.IsNotExist(err) {
		t.Errorf("%s is still in place: %v", store.ImportFile, err)
	}
	if _, err := os.Stat(filepath.Join(dir, store.Imported)); err != nil {
		t.Errorf("no %s: %v", store.Imported, err)
	}
	if err := db.Slack().SetCursor(ctx, "viewer_team", "T000BBB"); err != nil {
		t.Fatal(err)
	}
	db.Close()

	plantImport(t, dir)
	again, err := Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer again.Close()
	if team, err := again.Slack().Cursor(ctx, "viewer_team"); err != nil || team != "T000BBB" {
		t.Fatalf("a second start imported again: cursor %q %v", team, err)
	}
}
