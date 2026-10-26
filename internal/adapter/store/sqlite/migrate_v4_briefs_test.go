package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/domain/unit"
)

// v3Store is a store built from the frozen v3 schema with seed run on it,
// then migrated by this release.
func v3Store(t *testing.T, seed ...string) (*DB, string) {
	t.Helper()
	frozen, err := os.ReadFile(filepath.Join("testdata", "schema-v3.sql"))
	if err != nil {
		t.Fatal(err)
	}
	path := storeAt(t, schemaSet{3, string(frozen), current.migrations[:2]})
	rawExec(t, path, seed...)
	if _, err := current.migrate(context.Background(), path, migrateOpts(path)); err != nil {
		t.Fatal(err)
	}
	db, err := current.open(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db, path
}

func tableRows(t *testing.T, db *DB, q string) []string {
	t.Helper()
	rows, err := db.r.Query(q)
	if err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	defer rows.Close()
	cols, _ := rows.Columns()
	vals := make([]sql.NullString, len(cols))
	ptrs := make([]any, len(cols))
	for i := range vals {
		ptrs[i] = &vals[i]
	}
	var out []string
	for rows.Next() {
		if err := rows.Scan(ptrs...); err != nil {
			t.Fatal(err)
		}
		parts := make([]string, len(vals))
		for i, v := range vals {
			parts[i] = v.String
		}
		out = append(out, strings.Join(parts, "|"))
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func sameRows(t *testing.T, what string, got, want []string) {
	t.Helper()
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("%s:\ngot\n%s\nwant\n%s", what, strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestV4KeepsEveryPRBriefTagAndRun(t *testing.T) {
	t.Parallel()
	db, _ := v3Store(t,
		`INSERT INTO brief_runs (id, owner, repo, number, trigger, version, attempt_of, head_oid, body_edited_at, changed_files,
			started_at, ended_at, outcome, model, cost_usd, diff_bytes, diff_cut, brief, files, tags, input_sha)
			VALUES (7, 'acme', 'api', 12, 'background', 'b1-x', 'b1-x h1 2026-10-01T10:00:00Z 3', 'h1', '2026-10-01T10:00:00.000000000Z', 3,
			'2026-10-02T10:00:00.000000000Z', '2026-10-02T10:01:00.000000000Z', 'ok', 'm', 0.01, 900, 1, '{"goal":"g"}', '["a.go","b<c>.go"]', '["domain:billing"]', 'sha')`,
		`INSERT INTO brief_runs (id, owner, repo, number, trigger, version, attempt_of, head_oid, changed_files, started_at, outcome, error, files)
			VALUES (9, 'acme', 'web', 3, 'wanted', 'b1-x', 'b1-x h2 0001-01-01T00:00:00Z 0', 'h2', 5, '2026-10-03T10:00:00.000000000Z', 'failed', 'boom', '')`,
		`INSERT INTO pr_briefs (owner, repo, number, run, last_run) VALUES ('acme', 'api', 12, 7, 7)`,
		`INSERT INTO pr_briefs (owner, repo, number, failures, attempt_of, last_run, wanted_at, retry_at, gave_up_at, gave_up_reason)
			VALUES ('acme', 'web', 3, 3, 'b1-x h2 0001-01-01T00:00:00Z 0', 9, 'w', 'r', 'g', 'too many')`,
		`INSERT INTO pr_tags (owner, repo, number, key, value, run) VALUES ('acme', 'api', 12, 'domain', 'billing', 7)`,
		`INSERT INTO pr_tags (owner, repo, number, key, value, run) VALUES ('acme', 'api', 12, 'concern', 'perf', 7)`,
	)
	sameRows(t, "unit_briefs", tableRows(t, db, `SELECT * FROM unit_briefs ORDER BY provider, unit_key`), []string{
		"github|acme/api#12|0||7|7||||",
		"github|acme/web#3|3|b1-x h2 0001-01-01T00:00:00Z 0|0|9|w|r|g|too many",
	})
	sameRows(t, "unit_tags", tableRows(t, db, `SELECT * FROM unit_tags ORDER BY unit_key, key`), []string{
		"github|acme/api#12|concern|perf|7",
		"github|acme/api#12|domain|billing|7",
	})
	sameRows(t, "brief_runs", tableRows(t, db, `SELECT id, provider, unit_key, intent, trigger, version, attempt_of, revision, input,
		input_bytes, input_cut, outcome, error, brief, tags, input_sha FROM brief_runs ORDER BY id`), []string{
		`7|github|acme/api#12|brief|background|b1-x|b1-x h1 2026-10-01T10:00:00Z 3|h1 2026-10-01T10:00:00.000000000Z|{"changed_files":3,"files":["a.go","b<c>.go"]}|900|1|ok||{"goal":"g"}|["domain:billing"]|sha`,
		`9|github|acme/web#3|brief|wanted|b1-x|b1-x h2 0001-01-01T00:00:00Z 0|h2 |{"changed_files":5}||0|failed|boom|||`,
	})
	if got := tableRows(t, db, `SELECT seq FROM sqlite_sequence WHERE name = 'brief_runs'`); len(got) != 1 || got[0] != "9" {
		t.Errorf("the run ids' sequence: %v", got)
	}
}

func TestV4CopiesTheSlackBriefBeforeDroppingItsColumn(t *testing.T) {
	t.Parallel()
	db, _ := v3Store(t,
		`INSERT INTO extraction_cursors (provider, unit_key, brief, at) VALUES ('slack', 'C1/1700000000.000100', 'Ship the export', '2026-10-04T09:00:00.000000000Z')`,
		`INSERT INTO extraction_cursors (provider, unit_key, brief, at) VALUES ('slack', 'D1/1700000000.000200', 'Pick the venue', '2026-10-05T09:00:00.000000000Z')`,
		`INSERT INTO extraction_cursors (provider, unit_key, brief, at) VALUES ('slack', 'D1/1700000000.000300', 'Book the train', '2026-10-05T09:00:00.000000000Z')`,
		`INSERT INTO extraction_cursors (provider, unit_key, brief, at) VALUES ('slack', 'C2/1700000000.000400', '', '2026-10-06T09:00:00.000000000Z')`,
	)
	sameRows(t, "legacy runs", tableRows(t, db, `SELECT r.provider, r.unit_key, r.intent, r.trigger, r.version, r.outcome, r.started_at, r.ended_at, r.brief,
		b.run = r.id, b.last_run = r.id FROM brief_runs r JOIN unit_briefs b ON b.provider = r.provider AND b.unit_key = r.unit_key ORDER BY r.unit_key`), []string{
		`slack|C1/1700000000.000100|brief|legacy|legacy|ok|2026-10-04T09:00:00.000000000Z|2026-10-04T09:00:00.000000000Z|{"goal":"Ship the export"}|1|1`,
		`slack|D1/1700000000.000200|brief|legacy|legacy|ok|2026-10-05T09:00:00.000000000Z|2026-10-05T09:00:00.000000000Z|{"goal":"Pick the venue"}|1|1`,
		`slack|D1/1700000000.000300|brief|legacy|legacy|ok|2026-10-05T09:00:00.000000000Z|2026-10-05T09:00:00.000000000Z|{"goal":"Book the train"}|1|1`,
	})
	units := []unit.Unit{{Provider: "slack", Key: "C1/1700000000.000100"}, {Provider: "slack", Key: "D1/1700000000.000300"}, {Provider: "slack", Key: "C2/1700000000.000400"}}
	got, err := db.Extraction().States(context.Background(), units)
	if err != nil {
		t.Fatal(err)
	}
	for u, want := range map[unit.Unit]string{units[0]: `{"goal":"Ship the export"}`, units[1]: `{"goal":"Book the train"}`, units[2]: ""} {
		if got[u].Brief != want {
			t.Errorf("%s reads brief %q, want %q", u.Key, got[u].Brief, want)
		}
	}
}

func TestAV3ReleaseRefusesTheV4Store(t *testing.T) {
	t.Parallel()
	_, path := v3Store(t)
	db, err := handle(path+"?_pragma=query_only(1)", 1)
	if err != nil {
		t.Fatal(err)
	}
	var compatible string
	err = db.QueryRow(`SELECT value FROM meta WHERE key = ?`, compatibleKey).Scan(&compatible)
	db.Close()
	if err != nil || compatible != fmt.Sprint(SchemaVersion) {
		t.Fatalf("compatible version %q, %v", compatible, err)
	}
	frozen, err := os.ReadFile(filepath.Join("testdata", "schema-v3.sql"))
	if err != nil {
		t.Fatal(err)
	}
	v3 := schemaSet{3, string(frozen), current.migrations[:2]}
	if s := mustInspect(t, v3, path); s.stateFor(3) != StateNewer {
		t.Fatalf("a v3 release reads the migrated store as %v (%+v)", s.stateFor(3), s)
	}
	if _, err := v3.open(path, nil); err == nil {
		t.Fatal("a v3 release opened the v4 store")
	}
}
