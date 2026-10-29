package sqlite

import (
	"database/sql"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// v6PluginsFixture is a version 6 store holding a row of every plugin table, a GitHub override beside a Slack
// one, the host's and GitHub's meta keys, and a Slack unit's brief in use beside a GitHub unit's.
func v6PluginsFixture(t *testing.T) string {
	t.Helper()
	return v6Store(t,
		`INSERT INTO pull_requests (owner, repo, number, title, state, reasons) VALUES ('acme', 'api', 7, 'Fix the build', 'OPEN', '["author"]'),
			('acme', 'web', 9, 'Ship it', 'MERGED', '')`,
		`INSERT INTO pr_seen (owner, repo, number, at) VALUES ('acme', 'api', 7, '2026-10-01T00:00:00Z')`,
		`INSERT INTO pr_details (owner, repo, number, version, read_at, body) VALUES ('acme', 'api', 7, 3, '2026-10-01T00:00:00Z', '{}')`,
		`INSERT INTO fetches (workspace_id, at) VALUES ('ws-1', '2026-10-01T00:00:00Z')`,
		`INSERT INTO overrides (provider, unit_key, hold, taken_over, tracked) VALUES ('github', 'acme/api#7', 1, 1, 1),
			('slack', 'T1/C1/1.1', 1, 0, 0)`,
		`INSERT INTO meta (key, value) VALUES ('viewer:github', 'ana'), ('github_budget', '{}'), ('fast_sweep_at', 't'),
			('slow_sweep_at', 't'), ('scope_resolved', '[]'), ('explore_recent', '[]'), ('teams:github', '[]')`,
		`INSERT INTO segment_map (provider, conversation, segment_id, team, from_ts, created_at, updated_at) VALUES
			('slack', 'C1', 's1', 'T1', '1.1', 't', 't')`,
		`INSERT INTO extraction_cursors (provider, unit_key, at) VALUES ('slack', 'T1/C1/1.1', 't')`,
		`INSERT INTO extraction_hints (provider, unit_key, item_id) VALUES ('slack', 'T1/C1/1.1', 'i1')`,
		`INSERT INTO extraction_days (day, calls) VALUES ('2026-10-01', 2)`,
		`INSERT INTO extraction_runs (provider, unit_key, at, outcome) VALUES ('slack', 'T1/C1/1.1', 't', 'ok')`,
		`INSERT INTO slack_conversations (id, name) VALUES ('C1', 'general')`,
		`INSERT INTO slack_objects (object, conversation, kind) VALUES ('C1/1.1', 'C1', 'thread')`,
		`INSERT INTO slack_messages (conversation, ts, text) VALUES ('C1', '1.1', 'hello'), ('C1', '1.2', 'again')`,
		`INSERT INTO slack_reactions (conversation, ts, name, user) VALUES ('C1', '1.1', 'eyes', 'U1')`,
		`INSERT INTO slack_users (id, name) VALUES ('U1', 'bruno')`,
		`INSERT INTO slack_changes (conversation, ts, kind, at) VALUES ('C1', '1.1', 'new', 't')`,
		`INSERT INTO slack_cursors (name, value, at) VALUES ('socket', 'x', 't')`,
		`INSERT INTO brief_runs (id, trigger, version, attempt_of, started_at, outcome, brief, provider, unit_key) VALUES
			(1, 'extraction', 'v1', 'a', '2026-10-01T00:00:00Z', 'ok', '{"goal":"review"}', 'slack', 'T1/C1/1.1'),
			(2, 'wanted', 'v1', 'a', '2026-10-01T00:00:00Z', 'ok', '{"goal":"merge"}', 'github', 'acme/api#7')`,
		`INSERT INTO unit_briefs (provider, unit_key, run, last_run) VALUES ('slack', 'T1/C1/1.1', 1, 1), ('github', 'acme/api#7', 2, 2)`,
	)
}

func TestV7PluginStoresExportsAndDrops(t *testing.T) {
	t.Parallel()
	path := v6PluginsFixture(t)
	before := rawDB(t, path)
	counts := map[string]int{}
	for _, p := range v7Exports {
		for _, e := range p.tables {
			if e.ddl != "" {
				continue
			}
			counts[e.table] = rowCount(t, before, `SELECT count(*) FROM `+e.table)
		}
	}
	before.Close()
	migrateV6(t, path)
	core := rawDB(t, path)
	for _, p := range v7Exports {
		for _, e := range p.tables {
			if e.keep {
				continue
			}
			if got := strings1(t, core, `SELECT name FROM sqlite_schema WHERE name = ?`, e.table); len(got) > 0 {
				t.Errorf("the core still holds %s", e.table)
			}
		}
	}
	if got := strings1(t, core, `SELECT key FROM meta WHERE key IN ('viewer:github', 'teams:github', 'github_budget', 'fast_sweep_at',
		'slow_sweep_at', 'scope_resolved', 'explore_recent') ORDER BY key`); !slices.Equal(got, []string{"teams:github", "viewer:github"}) {
		t.Errorf("the core's meta keeps %q of GitHub's keys, want the host's viewer and teams", got)
	}
	if n := rowCount(t, core, `SELECT count(*) FROM overrides`); n != 2 {
		t.Errorf("the core keeps %d overrides, want both", n)
	}
	if n := rowCount(t, core, `SELECT count(*) FROM fetches`); n != 1 {
		t.Errorf("the core keeps %d fetches, want its workspace's", n)
	}
	state := filepath.Dir(path)
	github := exported(t, filepath.Join(state, "plugins", "github", "import.db"))
	for _, table := range []string{"pull_requests", "pr_seen", "pr_details", "fetches"} {
		if got := rowCount(t, github, `SELECT count(*) FROM `+table); got != counts[table] {
			t.Errorf("github's %s holds %d rows, the core held %d", table, got, counts[table])
		}
	}
	if got := strings1(t, github, `SELECT title FROM pull_requests ORDER BY number`); !slices.Equal(got, []string{"Fix the build", "Ship it"}) {
		t.Errorf("github's pull requests %q", got)
	}
	if got := strings1(t, github, `SELECT key FROM meta ORDER BY key`); !slices.Equal(got, []string{"explore_recent", "fast_sweep_at", "github_budget", "scope_resolved", "slow_sweep_at", "viewer:github"}) {
		t.Errorf("github's meta %q, want GitHub's keys alone", got)
	}
	if got := strings1(t, github, `SELECT provider || ' ' || unit_key || ' ' || taken_over || ' ' || tracked FROM overrides`); !slices.Equal(got, []string{"github acme/api#7 1 1"}) {
		t.Errorf("github's overrides %q", got)
	}
	slack := exported(t, filepath.Join(state, "plugins", "slack", "import.db"))
	for table, n := range counts {
		if !strings.HasPrefix(table, "slack_") && !strings.HasPrefix(table, "extraction_") && table != "segment_map" {
			continue
		}
		if got := rowCount(t, slack, `SELECT count(*) FROM `+table); got != n {
			t.Errorf("slack's %s holds %d rows, the core held %d", table, got, n)
		}
	}
	if got := strings1(t, slack, `SELECT provider || ' ' || unit_key || ' ' || brief FROM extraction_briefs`); !slices.Equal(got, []string{`slack T1/C1/1.1 {"goal":"review"}`}) {
		t.Errorf("slack's briefs %q, want its unit's brief in use", got)
	}
	for _, f := range []string{filepath.Join(state, "plugins", "github", "import.db"), filepath.Join(state, "plugins", "slack", "import.db")} {
		if fi, err := os.Stat(f); err != nil || fi.Mode().Perm() != 0o600 {
			t.Errorf("%s: %v %v, want 0600", f, fi.Mode().Perm(), err)
		}
	}
}

func TestCoreSchemaHasNoPluginTables(t *testing.T) {
	t.Parallel()
	path := v6PluginsFixture(t)
	migrateV6(t, path)
	for name, db := range map[string]*sql.DB{"a new store": rawDB(t, storeAt(t, current)), "a migrated store": rawDB(t, path)} {
		for _, table := range strings1(t, db, `SELECT name FROM sqlite_schema WHERE type = 'table'`) {
			if pluginTable(table) {
				t.Errorf("%s holds the plugin table %s", name, table)
			}
		}
	}
}

// pluginTables are the version 6 tables that belong to the plugins alone, named here rather than read from
// v7Exports so a table the export wrongly keeps fails the test.
var pluginTables = []string{"pull_requests", "pr_seen", "pr_details", "segment_map",
	"extraction_cursors", "extraction_hints", "extraction_days", "extraction_runs",
	"slack_conversations", "slack_objects", "slack_messages", "slack_reactions", "slack_users", "slack_changes", "slack_cursors"}

func pluginTable(table string) bool { return slices.Contains(pluginTables, table) }

func exported(t *testing.T, path string) *sql.DB {
	t.Helper()
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
	return rawDB(t, path)
}

func rowCount(t *testing.T, db *sql.DB, q string, args ...any) int {
	t.Helper()
	var n int
	if err := db.QueryRow(q, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	return n
}

// TestAnExportIsDurableBeforeTheCoreDropsItsTables: the export commits to
// import.db, checked against the core's rows, before the migration's own
// transaction drops the tables; a failure between the two leaves both
// copies, and the next migration keeps the complete export.
func TestAnExportIsDurableBeforeTheCoreDropsItsTables(t *testing.T) {
	t.Parallel()
	path := v6PluginsFixture(t)
	state := filepath.Dir(path)
	failDropIn(t, state)
	if _, err := current.migrate(t.Context(), path, migrateOpts(path)); err == nil {
		t.Fatal("the migration went through its injected fault")
	}
	if v := versionOf(t, path); v != 6 {
		t.Fatalf("the failed migration left the store at version %d", v)
	}
	core := rawDB(t, path)
	if n := rowCount(t, core, `SELECT count(*) FROM pull_requests`); n != 2 {
		t.Errorf("the core holds %d pull requests after the failure, want both", n)
	}
	core.Close()
	exports := map[string]os.FileInfo{}
	for id, table := range map[string]string{"github": "pull_requests", "slack": "slack_messages"} {
		file := filepath.Join(state, "plugins", id, "import.db")
		db := exported(t, file)
		if n := rowCount(t, db, `SELECT count(*) FROM `+table); n != 2 {
			t.Errorf("%s's export holds %d rows of %s after the failure, want 2", id, n, table)
		}
		db.Close()
		fi, err := os.Stat(file)
		if err != nil {
			t.Fatal(err)
		}
		exports[id] = fi
		if _, err := os.Stat(file + ".tmp"); err == nil {
			t.Errorf("%s's export left its temporary file", id)
		}
	}

	healDrop(state)
	migrateV6(t, path)
	for id, before := range exports {
		after, err := os.Stat(filepath.Join(state, "plugins", id, "import.db"))
		if err != nil || !after.ModTime().Equal(before.ModTime()) || after.Size() != before.Size() {
			t.Errorf("the second migration rewrote %s's complete export: %v", id, err)
		}
	}
	if got := strings1(t, rawDB(t, path), `SELECT name FROM sqlite_schema WHERE name = 'pull_requests'`); len(got) > 0 {
		t.Error("the second migration left the core's pull requests")
	}
}
