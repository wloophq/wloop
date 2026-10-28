package sqlite

import (
	"context"
	"slices"
	"testing"
)

// retainBriefs runs the brief retention retain.go's BriefBodies rule runs.
func retainBriefs(t *testing.T, db *DB, before string) Retained {
	t.Helper()
	out := Retained{}
	cursors, err := pluginRows{db}.cursorUnits(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Write(context.Background(), func(tx *Tx) error {
		r := &retainer{ctx: context.Background(), tx: tx, out: out}
		r.briefs(before, cursors)
		return r.err
	}); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestASlackBriefGoesWithItsCursor(t *testing.T) {
	t.Parallel()
	db, _ := openTemp(t)
	run := func(id int, unitKey string, started string) {
		exec(t, db, `INSERT INTO brief_runs (id, provider, unit_key, intent, trigger, version, attempt_of, started_at, ended_at, outcome, brief)
			VALUES (?, 'slack', ?, 'brief', 'extraction', 'extraction', '', ?, ?, 'ok', '{"goal":"g"}')`, id, unitKey, started, started)
	}
	old, fresh := at(retainNow.Add(-8*day)), at(retainNow.Add(-day))
	exec(t, db, `INSERT INTO extraction_cursors (provider, unit_key, at) VALUES ('slack', 'C1/1', ?)`, fresh)
	run(1, "C1/1", old)
	run(2, "C1/1", old)
	run(3, "C1/1", old)
	run(4, "C1/1", fresh)
	exec(t, db, `INSERT INTO unit_briefs (provider, unit_key, run, last_run) VALUES ('slack', 'C1/1', 4, 4)`)
	exec(t, db, `INSERT INTO turns (provider, unit_key, intent, turn_id, state, sent_at, brief_run) VALUES ('slack', 'C1/1', 'act', 't1', 'done', ?, 2)`, old)
	run(5, "C2/2", old)
	run(6, "C2/2", fresh)
	exec(t, db, `INSERT INTO unit_briefs (provider, unit_key, run, last_run) VALUES ('slack', 'C2/2', 6, 6)`)
	exec(t, db, `INSERT INTO brief_runs (id, provider, unit_key, intent, trigger, version, attempt_of, started_at, outcome)
		VALUES (7, 'github', 'acme/api#1', 'brief', 'wanted', 'v', '', ?, 'ok')`, old)

	got := retainBriefs(t, db, at(retainNow.Add(-7*day)))
	if got["unit_briefs"] != 1 {
		t.Errorf("retained %v", got)
	}
	if left := column(t, db, `SELECT id FROM brief_runs ORDER BY id`); !slices.Equal(left, []string{"2", "4", "7"}) {
		t.Errorf("runs left %q, want the turn's 2, the brief in use 4 and the pull request's 7", left)
	}
	if left := column(t, db, `SELECT unit_key FROM unit_briefs`); !slices.Equal(left, []string{"C1/1"}) {
		t.Errorf("briefs left %q", left)
	}
	if again := retainBriefs(t, db, at(retainNow.Add(-7*day))); len(again) != 0 {
		t.Errorf("a second pass retained %v", again)
	}
}

func TestACursorOfAPullRequestLeavesItsBriefs(t *testing.T) {
	t.Parallel()
	db, _ := openTemp(t)
	old, fresh := at(retainNow.Add(-8*day)), at(retainNow.Add(-day))
	exec(t, db, `INSERT INTO extraction_cursors (provider, unit_key, at) VALUES ('github', 'acme/api#9', ?)`, fresh)
	for id, trigger := range map[int]string{1: "wanted", 2: "fields"} {
		exec(t, db, `INSERT INTO brief_runs (id, provider, unit_key, intent, trigger, version, attempt_of, started_at, outcome, brief)
			VALUES (?, 'github', 'acme/api#1', 'brief', ?, 'v', '', ?, 'ok', '{"goal":"g"}')`, id, trigger, old)
	}
	exec(t, db, `INSERT INTO unit_briefs (provider, unit_key, run, last_run) VALUES ('github', 'acme/api#1', 1, 2)`)

	if got := retainBriefs(t, db, at(retainNow.Add(-7*day))); len(got) != 0 {
		t.Errorf("retained %v", got)
	}
	if left := column(t, db, `SELECT id FROM brief_runs ORDER BY id`); !slices.Equal(left, []string{"1", "2"}) {
		t.Errorf("runs left %q", left)
	}
	if left := column(t, db, `SELECT count(*) FROM unit_briefs`); !slices.Equal(left, []string{"1"}) {
		t.Errorf("briefs left %q", left)
	}
}
