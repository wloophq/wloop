package sqlite

import (
	"context"
	"database/sql"
)

// v7Wakes makes every snooze and mute a wake, then drops their columns from tasks and overrides.
//
// A deferred task's snooze becomes its wake: its until, its return state, and on activity for one that waited
// on activity, as a mute until the state changed did. An open task that holds a unit the overrides mute and
// that no snooze deferred yet is deferred into the first deferred state under a wake of the mute, returning
// it to its state. A mute of a unit no open task holds has no task to wake and goes with its columns.
func v7Wakes(ctx context.Context, tx *sql.Tx) error {
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{sql: `CREATE TABLE wakes (
			task_id TEXT PRIMARY KEY REFERENCES tasks (id) ON DELETE CASCADE,
			until TEXT NOT NULL DEFAULT '', on_kinds TEXT NOT NULL DEFAULT '[]', actors TEXT NOT NULL DEFAULT '[]',
			cond TEXT NOT NULL DEFAULT '', expires TEXT NOT NULL DEFAULT '', return_state TEXT NOT NULL DEFAULT ''
		) WITHOUT ROWID`},
		{sql: `INSERT INTO wakes (task_id, until, on_kinds, return_state)
			SELECT id, snooze_until, iif(snooze_on_activity != 0, '["activity"]', '[]'), snooze_return FROM tasks
			WHERE category = 'deferred' AND (snooze_until != '' OR snooze_on_activity != 0 OR snooze_mute != 0)`},
		{sql: `CREATE TEMP TABLE v7_muted AS
			SELECT t.id, t.state, max(o.muted_until) AS muted_until, max(o.mute_until_state) AS mute_until_state
			FROM tasks t JOIN overrides o
			ON t.id IN (SELECT task_id FROM task_units WHERE provider = o.provider AND unit_key = o.unit_key
				UNION SELECT task_id FROM task_items WHERE provider = o.provider AND unit_key = o.unit_key)
			WHERE (o.muted_until != '' OR o.mute_until_state != 0) AND t.category NOT IN ('done', 'dropped')
				AND t.id NOT IN (SELECT task_id FROM wakes)
			GROUP BY t.id`},
		{sql: `INSERT INTO wakes (task_id, until, on_kinds, return_state)
			SELECT id, iif(mute_until_state != 0, '', muted_until), iif(mute_until_state != 0, '["activity"]', '[]'), state
			FROM v7_muted`},
		{sql: `UPDATE tasks SET state = ?, category = 'deferred' WHERE id IN (SELECT id FROM v7_muted)`, args: []any{v6FirstOf("deferred")}},
		{sql: `DROP TABLE v7_muted`},
		{sql: `ALTER TABLE tasks DROP COLUMN snooze_until`},
		{sql: `ALTER TABLE tasks DROP COLUMN snooze_on_activity`},
		{sql: `ALTER TABLE tasks DROP COLUMN snooze_return`},
		{sql: `ALTER TABLE tasks DROP COLUMN snooze_mute`},
		{sql: `ALTER TABLE overrides DROP COLUMN muted_until`},
		{sql: `ALTER TABLE overrides DROP COLUMN mute_until_state`},
		{sql: `ALTER TABLE overrides DROP COLUMN muted_kind`},
		{sql: `ALTER TABLE overrides DROP COLUMN muted_waiting_on`},
	} {
		if _, err := tx.ExecContext(ctx, q.sql, q.args...); err != nil {
			return err
		}
	}
	return nil
}

// v6FirstOf is the first version 6 state of category c.
func v6FirstOf(c string) string {
	for _, s := range v6States {
		if s.category == c {
			return s.state
		}
	}
	return ""
}
