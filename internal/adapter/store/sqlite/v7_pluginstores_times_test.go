package sqlite

import (
	"path/filepath"
	"testing"
	"time"

	ghstore "github.com/wloophq/wloop/plugins/github/store"
)

// TestTheGitHubExportHoldsTimesThePluginParses: version 6 wrote some
// times with a trimmed fraction; the export writes them in the layout the
// GitHub plugin reads, so its import parses every row.
func TestTheGitHubExportHoldsTimesThePluginParses(t *testing.T) {
	t.Parallel()
	path := v6Store(t,
		`INSERT INTO pull_requests (owner, repo, number, title, state, created_at, updated_at) VALUES
			('acme', 'api', 7, 'Fix the build', 'OPEN', '2026-09-30T10:00:00Z', '2026-10-01T00:00:00.5Z')`,
		`INSERT INTO pr_seen (owner, repo, number, at) VALUES ('acme', 'api', 7, '2026-10-01T00:00:00.123456Z')`,
		`INSERT INTO pr_details (owner, repo, number, version, read_at, body) VALUES ('acme', 'api', 7, 1, '2026-10-01T00:00:01.25Z', '{}')`,
		`INSERT INTO fetches (workspace_id, at) VALUES ('ws-1', '2026-10-01T00:00:02.000001Z')`,
	)
	migrateV6(t, path)
	export := exported(t, filepath.Join(filepath.Dir(path), "plugins", "github", "import.db"))
	for q, want := range map[string]string{
		`SELECT updated_at FROM pull_requests`: "2026-10-01T00:00:00.500000000Z",
		`SELECT created_at FROM pull_requests`: "2026-09-30T10:00:00.000000000Z",
		`SELECT at FROM pr_seen`:               "2026-10-01T00:00:00.123456000Z",
		`SELECT read_at FROM pr_details`:       "2026-10-01T00:00:01.250000000Z",
		`SELECT at FROM fetches`:               "2026-10-01T00:00:02.000001000Z",
	} {
		if got := strings1(t, export, q); len(got) != 1 || got[0] != want {
			t.Errorf("%s: %q, want %q", q, got, want)
		}
	}
	export.Close()
	db, err := ghstore.Open(t.Context(), filepath.Join(filepath.Dir(path), "plugins", "github"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	prs, err := db.ListPRs(t.Context(), false)
	if err != nil {
		t.Fatalf("the plugin reads its import: %v", err)
	}
	if len(prs) != 1 || !prs[0].UpdatedAt.Equal(time.Date(2026, 10, 1, 0, 0, 0, 500_000_000, time.UTC)) {
		t.Errorf("the plugin read %+v", prs)
	}
}
