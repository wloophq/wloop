package sqlite

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/domain/round"
)

func TestVersion3MovesWhatRoundsHandledIntoMarks(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	frozen, err := os.ReadFile(filepath.Join("testdata", "schema-v2.sql"))
	if err != nil {
		t.Fatal(err)
	}
	path := storeAt(t, schemaSet{2, string(frozen), nil})
	rawExec(t, path,
		`INSERT INTO round_state (provider, unit_key, fingerprint, checks_head, agent_home) VALUES ('github', 'acme/api#1', '2026-10-08T10:00:00Z@h1', 'h1', '/h')`,
		`INSERT INTO round_state (provider, unit_key, fingerprint, checks_head) VALUES ('github', 'acme/api#2', '', 'h2')`,
		`INSERT INTO round_state (provider, unit_key, sent_at) VALUES ('github', 'acme/api#3', 'x')`,
		`INSERT INTO round_state (provider, unit_key, fingerprint) VALUES ('github', 'acme/api#4', '2026-10-08T10:00:00Z')`,
	)
	if _, err := current.migrate(ctx, path, migrateOpts(path)); err != nil {
		t.Fatal(err)
	}
	db, err := current.open(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	rows, err := db.r.Query(`SELECT unit_key, mark, value FROM unit_marks ORDER BY unit_key, mark`)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for rows.Next() {
		var u, m, v string
		if err := rows.Scan(&u, &m, &v); err != nil {
			t.Fatal(err)
		}
		got = append(got, strings.Join([]string{u, m, v}, " "))
	}
	rows.Close()
	want := []string{
		"acme/api#1 addressed.checks h1",
		"acme/api#1 reviewed 2026-10-08T10:00:00Z@h1",
		"acme/api#2 addressed.checks h2",
		"acme/api#4 addressed.reviews 2026-10-08T10:00:00Z",
		"acme/api#4 reviewed 2026-10-08T10:00:00Z",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("got\n%s", strings.Join(got, "\n"))
	}
	st, err := NewRounds(db).Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if p := st.PRs["acme/api#1"]; p.Marks[round.AddressedChecks] != "h1" || p.AgentHome != "/h" {
		t.Errorf("loaded %+v", p)
	}
	if p := st.PRs["acme/api#3"]; len(p.Marks) != 0 {
		t.Errorf("a row without marks loaded %v", p.Marks)
	}
}
