package sqlite

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestV4RewritesTheJobsDraftsToTheirWorkflows(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	frozen, err := os.ReadFile(filepath.Join("testdata", "schema-v3.sql"))
	if err != nil {
		t.Fatal(err)
	}
	path := storeAt(t, schemaSet{3, string(frozen), nil})
	var stmts []string
	for _, d := range []struct{ origin, rule, key, kind, state string }{
		{"auto", "stalled-nudge", "e-1:slack:U1", "nudge", "proposed"},
		{"auto", "stalled-nudge", "e-1:slack:U2", "nudge", "sent"},
		{"auto", "closure", "slack:T1/C1/1.1", "close", "sent"},
		{"auto", "closure", "slack:T1/C1/1.2", "close", "discarded"},
		{"auto", "ask-owner", "slack:T1/C1/1.3", "ask_owner", "failed"},
		{"auto", "github-nudge", "github:o/r#1", "nudge", "proposed"},
		{"user", "", "", "reply", "proposed"},
	} {
		stmts = append(stmts, `INSERT INTO drafts (provider, unit_key, kind, origin_kind, rule, rule_key, state, created_at, updated_at)
			VALUES ('slack', 'T1/C1/1.1', '`+d.kind+`', '`+d.origin+`', '`+d.rule+`', '`+d.key+`', '`+d.state+`', '2026-10-01T10:00:00Z', '2026-10-02T10:00:00Z')`)
	}
	rawExec(t, path, stmts...)
	if _, err := current.migrate(ctx, path, migrateOpts(path)); err != nil {
		t.Fatal(err)
	}
	db, err := current.open(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	rows, err := db.r.Query(`SELECT rule, workflow, rule_key, state, effects FROM drafts ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for rows.Next() {
		var r, w, k, s, e string
		if err := rows.Scan(&r, &w, &k, &s, &e); err != nil {
			t.Fatal(err)
		}
		got = append(got, r+" "+w+" "+k+" "+s+" "+e)
	}
	want := []string{
		`slack-stalled-nudge slack-stalled-nudge e-1:slack:U1 held ["task.nudged"]`,
		`slack-stalled-nudge slack-stalled-nudge e-1:slack:U2 sent []`,
		`slack-closure slack-closure slack:T1/C1/1.1 sent []`,
		`slack-closure slack-closure slack:T1/C1/1.2 dropped ["unit.closed"]`,
		`slack-ask-owner slack-ask-owner slack:T1/C1/1.3 failed []`,
		`github-nudge  github:o/r#1 held ["task.nudged"]`,
		`   held []`,
	}
	if !slices.Equal(got, want) {
		t.Fatalf("drafts after v4:\n%q\nwant\n%q", got, want)
	}
}
