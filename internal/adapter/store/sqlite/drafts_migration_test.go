package sqlite

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestVersion2ShapesDraftsFromTheirKind(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	frozen, err := os.ReadFile(filepath.Join("testdata", "schema-v1.sql"))
	if err != nil {
		t.Fatal(err)
	}
	path := storeAt(t, schemaSet{1, string(frozen), nil})
	var stmts []string
	for _, d := range []struct{ unit, kind, state string }{
		{"C1/1", "nudge", "proposed"}, {"C1/2", "nudge", "sent"}, {"C1/3", "close", "failed"}, {"C1/4", "close", "sent"},
		{"C1/5", "react", "proposed"}, {"C1/6", "comment", "approved"}, {"C1/7", "reply", "proposed"}, {"C1/8", "ask_owner", "proposed"},
	} {
		stmts = append(stmts, `INSERT INTO drafts (provider, unit_key, kind, origin_kind, state, created_at, updated_at) VALUES ('slack', '`+d.unit+`', '`+d.kind+`', 'auto', '`+d.state+`', 'x', 'x')`)
	}
	for _, u := range []string{"C1/4", "C1/3"} {
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
	rows, err := db.r.Query(`SELECT unit_key, purpose, delivery, effects FROM drafts ORDER BY unit_key`)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for rows.Next() {
		var u, p, d, e string
		if err := rows.Scan(&u, &p, &d, &e); err != nil {
			t.Fatal(err)
		}
		got = append(got, strings.Join([]string{u, p, d, e}, " "))
	}
	rows.Close()
	want := []string{
		`C1/1 nudge message ["entry.nudged"]`, `C1/2 nudge message []`, `C1/3 close message ["unit.closed"]`, `C1/4 close message []`,
		`C1/5 react reaction []`, `C1/6 comment comment []`, `C1/7 reply message []`, `C1/8 ask_owner message []`,
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("got\n%s", strings.Join(got, "\n"))
	}
	var closed string
	if err := db.r.QueryRow(`SELECT group_concat(unit_key) FROM extraction_cursors WHERE closure_posted = 1`).Scan(&closed); err != nil || closed != "C1/4" {
		t.Errorf("closure_posted on %q %v", closed, err)
	}
}

func TestADraftAnOlderReleaseWroteReadsWithTheShapeOfItsKind(t *testing.T) {
	t.Parallel()
	db, path := openTemp(t)
	db.Close()
	rawExec(t, path, `INSERT INTO drafts (provider, unit_key, kind, origin_kind, state, created_at, updated_at) VALUES ('slack', 'C1/1', 'react', 'auto', 'proposed', '', '')`)
	db, err := Open(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	d, err := db.Drafts().Get(context.Background(), 1)
	if err != nil || d.Purpose != "react" || d.Delivery != "reaction" || d.Effects == nil {
		t.Fatalf("%+v %v", d, err)
	}
}
