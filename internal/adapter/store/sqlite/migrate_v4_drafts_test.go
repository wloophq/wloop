package sqlite

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestV4MarksClosuresAndAppliedEffects(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	frozen, err := os.ReadFile(filepath.Join("testdata", "schema-v3.sql"))
	if err != nil {
		t.Fatal(err)
	}
	path := storeAt(t, schemaSet{3, string(frozen), nil})
	var stmts []string
	stmts = append(stmts, `INSERT INTO drafts (provider, unit_key, kind, origin_kind, state, effects, created_at, updated_at)
			VALUES ('github', 'o/r#1', 'nudge', 'auto', 'proposed', '["entry.nudged"]', '2026-10-01T10:00:00Z', '2026-10-02T10:00:00Z')`)
	for _, d := range []struct{ unit, kind, state, effects string }{
		{"C1/1", "nudge", "sent", "[]"}, {"C1/2", "close", "sent", "[]"}, {"C1/3", "nudge", "proposed", "[]"},
		{"C1/4", "close", "failed", "[]"}, {"C1/5", "react", "sent", `["unit.closed"]`}, {"C1/6", "reply", "sent", "[]"},
		{"C1/7", "nudge", "approved", `["entry.nudged"]`}, {"C1/8", "close", "discarded", "[]"},
	} {
		stmts = append(stmts, `INSERT INTO drafts (provider, unit_key, kind, origin_kind, state, effects, created_at, updated_at)
			VALUES ('slack', '`+d.unit+`', '`+d.kind+`', 'auto', '`+d.state+`', '`+d.effects+`', '2026-10-01T10:00:00Z', '2026-10-02T10:00:00Z')`)
	}
	for _, u := range []string{"C1/2", "C1/4", "C1/5", "C1/6"} {
		stmts = append(stmts, `INSERT INTO extraction_cursors (provider, unit_key, at) VALUES ('slack', '`+u+`', 'x')`)
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
	rows, err := db.r.Query(`SELECT unit_key, state, effects, effects_applied_at FROM drafts ORDER BY unit_key`)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for rows.Next() {
		var u, s, e, a string
		if err := rows.Scan(&u, &s, &e, &a); err != nil {
			t.Fatal(err)
		}
		got = append(got, strings.TrimSpace(strings.Join([]string{u, s, e, a}, " ")))
	}
	rows.Close()
	want := []string{
		`C1/1 sent [] 2026-10-02T10:00:00Z`, `C1/2 sent [] 2026-10-02T10:00:00Z`, `C1/3 held ["task.nudged"]`,
		`C1/4 failed ["unit.closed"]`, `C1/5 sent ["unit.closed"] 2026-10-02T10:00:00Z`, `C1/6 sent [] 2026-10-02T10:00:00Z`,
		`C1/7 queued ["task.nudged"]`, `C1/8 dropped []`, `o/r#1 held ["task.nudged","pr.nudged"]`,
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("drafts\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	var marked string
	slack := exported(t, filepath.Join(filepath.Dir(path), "plugins", "slack", "import.db"))
	if err := slack.QueryRow(`SELECT group_concat(unit_key) FROM (SELECT unit_key FROM extraction_cursors WHERE closure_marked = 1 ORDER BY unit_key)`).Scan(&marked); err != nil || marked != "C1/2,C1/5" {
		t.Errorf("closure_marked on %q %v", marked, err)
	}
	pending, err := db.DraftEffects().Unapplied(ctx)
	if err != nil || len(pending) != 0 {
		t.Errorf("a draft sent before v4 waits for its effects: %+v %v", pending, err)
	}
}

func TestV4RenamesTheDraftApprovalModesAndWhatTheyRecorded(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	frozen, err := os.ReadFile(filepath.Join("testdata", "schema-v3.sql"))
	if err != nil {
		t.Fatal(err)
	}
	path := storeAt(t, schemaSet{3, string(frozen), nil})
	var stmts []string
	for _, d := range []struct{ unit, mode, state, reason, policy, outcome string }{
		{"C1/1", "always", "proposed", "", "hold", ""}, {"C1/2", "window", "proposed", "", "send", ""},
		{"C1/3", "none", "sent", "", "send", "auto"}, {"C1/4", "observe", "discarded", "observe", "send", "observed"},
		{"C1/5", "always", "discarded", "user", "hold", "discarded"}, {"C1/6", "", "sent", "", "", ""},
	} {
		stmts = append(stmts, `INSERT INTO drafts (provider, unit_key, kind, origin_kind, state, approval_mode, reason, would_policy, would_outcome, created_at, updated_at)
			VALUES ('slack', '`+d.unit+`', 'reply', 'auto', '`+d.state+`', '`+d.mode+`', '`+d.reason+`', '`+d.policy+`', '`+d.outcome+`', '2026-10-01T10:00:00Z', '2026-10-02T10:00:00Z')`)
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
	rows, err := db.r.Query(`SELECT d.unit_key, d.state, d.reason, g.status, g.by_actor, g.rule, g.default_intent
		FROM drafts d JOIN gates g ON g.kind = 'draft' AND g.subject = CAST(d.id AS TEXT) ORDER BY d.unit_key`)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for rows.Next() {
		var u, st, r, gs, by, rule, def string
		if err := rows.Scan(&u, &st, &r, &gs, &by, &rule, &def); err != nil {
			t.Fatal(err)
		}
		got = append(got, strings.Join([]string{u, st, r, gs, by, rule, def}, "|"))
	}
	rows.Close()
	want := []string{"C1/1|held||open|||", "C1/2|held||open|||approve", "C1/3|sent||approved|workflow|send|",
		"C1/4|dropped|dry_run|rejected|workflow|dry_run|", "C1/5|dropped|user|rejected|person||", "C1/6|sent||approved|agent||"}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("drafts\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}
