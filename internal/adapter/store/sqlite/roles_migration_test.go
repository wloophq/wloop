package sqlite

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestV4BackfillsTheTurnsRoles(t *testing.T) {
	t.Parallel()
	frozen, err := os.ReadFile(filepath.Join("testdata", "schema-v3.sql"))
	if err != nil {
		t.Fatal(err)
	}
	path := storeAt(t, schemaSet{3, string(frozen), nil})
	var stmts []string
	for i, intent := range []string{"review", "address", "nudge", "probe", "act", "my-review"} {
		stmts = append(stmts, `INSERT INTO turns (provider, unit_key, intent, turn_id, state, sent_at) VALUES ('github', 'acme/api#7', '`+intent+`', 't`+string(rune('a'+i))+`', 'done', 'x')`)
	}
	rawExec(t, path, stmts...)
	if _, err := current.migrate(context.Background(), path, migrateOpts(path)); err != nil {
		t.Fatal(err)
	}
	db, err := current.open(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var got string
	if err := db.r.QueryRow(`SELECT group_concat(intent || '=' || role, ' ') FROM (SELECT intent, role FROM turns ORDER BY seq)`).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if want := "review=round.review address=round.address nudge=nudge probe=probe act=headless.default my-review="; got != want {
		t.Fatalf("roles %q, want %q", got, want)
	}
}
