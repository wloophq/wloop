package sqlite

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestVersion6LinksEachAnswerToItsMessage(t *testing.T) {
	t.Parallel()
	frozen, err := os.ReadFile(filepath.Join("testdata", "schema-v5.sql"))
	if err != nil {
		t.Fatal(err)
	}
	path := storeAt(t, schemaSet{5, string(frozen), nil})
	answer := func(id, seq, at, route string) string {
		return `INSERT INTO turn_answers (id, turn_seq, provider, unit_key, at, text, source, route) VALUES (` + id + `, ` + seq + `, 'slack', 'C1/1', '` + at + `', 'a` + id + `', 'cockpit', '` + route + `')`
	}
	msg := func(id, origin, seq, state, closed string) string {
		return `INSERT INTO messages (id, provider, unit_key, kind, text, origin_kind, turn_seq, state, created_at, closed_at) VALUES (` + id + `, 'slack', 'C1/1', 'question', 'q', '` + origin + `', ` + seq + `, '` + state + `', '2026-01-01T09:00:00Z', '` + closed + `')`
	}
	rawExec(t, path,
		// Two posts of turn 1 answered in reverse order, each closed a moment after its answer.
		answer("1", "1", "2026-01-01T10:00:00Z", "typed"),
		answer("2", "1", "2026-01-01T10:05:00Z", "queued"),
		msg("11", "agent", "1", "answered", "2026-01-01T10:05:01Z"),
		msg("10", "agent", "1", "answered", "2026-01-01T10:00:01Z"),
		// Turn 2's question: a refused answer the newer one replaced, a direct one typed in the terminal, then the answer.
		answer("3", "2", "2026-01-01T11:00:00Z", "refused"),
		answer("4", "2", "2026-01-01T11:01:00Z", "direct"),
		answer("5", "2", "2026-01-01T11:02:00Z", "resumed"),
		msg("12", "turn", "2", "answered", "2026-01-01T11:03:00Z"),
		// Turn 3 has an answer and no message that matches it.
		answer("6", "3", "2026-01-01T12:00:00Z", "typed"),
		msg("13", "turn", "3", "superseded", "2026-01-01T12:01:00Z"),
	)
	if _, err := current.migrate(context.Background(), path, migrateOpts(path)); err != nil {
		t.Fatal(err)
	}
	db, err := current.open(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	rows, err := db.r.Query(`SELECT id || '>' || message_id || ' ' || (read_at != '') FROM turn_answers ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatal(err)
		}
		got = append(got, s)
	}
	rows.Close()
	want := []string{"1>10 1", "2>11 0", "3>0 1", "4>0 1", "5>12 1", "6>0 1"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("answer>message read: got %v, want %v", got, want)
	}
}
