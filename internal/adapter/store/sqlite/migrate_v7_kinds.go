package sqlite

import (
	"cmp"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
)

// v7Kinds ends the task kinds of version 6. A waiting task becomes one task per item it grouped, the oldest
// item's keeping its id; the others are aliased to it. A close or message task gives its rows, gates among
// them, to the task holding its unit and is dropped; with no such task it stays as the task the gates service
// would have opened for the unit. Every task gets the kind property, stored items propose categories, and the
// times the store reads in event.TimeLayout are written in it. tasks.waiting and tasks.kind go.
func v7Kinds(ctx context.Context, tx *sql.Tx) error {
	if _, err := tx.ExecContext(ctx, `CREATE TABLE task_aliases (
  old_id TEXT NOT NULL, new_id TEXT NOT NULL,
  PRIMARY KEY (old_id, new_id)
) WITHOUT ROWID`); err != nil {
		return err
	}
	for _, step := range []func(context.Context, *sql.Tx) error{
		v7FixedTimes, v7ItemCategories, v7AutoStars, v7WaitingProps, v7SplitWaiting, v7EndCloseAndMessageTasks, v7MissingKinds,
	} {
		if err := step(ctx, tx); err != nil {
			return err
		}
	}
	for _, q := range []string{`ALTER TABLE tasks DROP COLUMN waiting`, `ALTER TABLE tasks DROP COLUMN kind`} {
		if _, err := tx.ExecContext(ctx, q); err != nil {
			return fmt.Errorf("%s: %w", q, err)
		}
	}
	return nil
}

// v7FixedTimeColumns are the columns the store reads in event.TimeLayout. Version 6 wrote some of them, and
// the briefs step copies brief_runs.ended_at into events.at, in time.RFC3339Nano, which trims the fraction.
var v7FixedTimeColumns = []struct{ table, column string }{
	{"events", "at"},
	{"tasks", "snooze_until"}, {"tasks", "seen_at"}, {"tasks", "created"}, {"tasks", "updated"}, {"tasks", "reviewed_at"},
	{"task_props", "at"},
	{"gates", "deadline"}, {"gates", "opened_at"}, {"gates", "closed_at"},
	{"plan_items", "at"}, {"plan_items", "saved_at"}, {"plan_items", "deadline"},
	{"workflow_runs", "at"}, {"workflow_cursors", "last_fire"}, {"fetches", "at"},
	{"intakes", "accepted_at"}, {"intakes", "briefing_at"}, {"intakes", "starting_at"}, {"intakes", "ended_at"},
	{"overrides", "muted_until"},
	{"messages", "created_at"}, {"messages", "closed_at"},
	{"brief_runs", "started_at"}, {"brief_runs", "ended_at"},
	{"unit_briefs", "wanted_at"}, {"unit_briefs", "retry_at"}, {"unit_briefs", "gave_up_at"},
	{"pull_requests", "created_at"}, {"pull_requests", "updated_at"}, {"pull_requests", "merged_at"},
	{"pull_requests", "closed_at"}, {"pull_requests", "body_edited_at"}, {"pull_requests", "mergeable_at"},
	{"pr_seen", "at"}, {"pr_details", "read_at"},
}

// v7FixedTimes rewrites every time of v7FixedTimeColumns in event.TimeLayout. A row the rewrite would make
// repeat another under a unique key is that row recorded twice, and goes.
func v7FixedTimes(ctx context.Context, tx *sql.Tx) error {
	fixedLen := len(eventTime(time.Unix(0, 0)))
	for _, c := range v7FixedTimeColumns {
		var n int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM pragma_table_info(?) WHERE name = ?`, c.table, c.column).Scan(&n); err != nil {
			return err
		}
		if n == 0 {
			continue
		}
		var vals []string
		err := eachRow(ctx, tx, `SELECT DISTINCT `+c.column+` FROM `+c.table+` WHERE `+c.column+` != '' AND (length(`+c.column+`) != ? OR substr(`+c.column+`, -1) != 'Z')`, //nolint:gosec // the names come from the table above
			[]any{fixedLen}, func(r *sql.Rows) error {
				var v string
				if err := r.Scan(&v); err != nil {
					return err
				}
				vals = append(vals, v)
				return nil
			})
		if err != nil {
			return fmt.Errorf("reading %s.%s: %w", c.table, c.column, err)
		}
		for _, v := range vals {
			t, err := time.Parse(time.RFC3339Nano, v)
			if err != nil {
				continue
			}
			for _, q := range []string{`UPDATE OR IGNORE ` + c.table + ` SET ` + c.column + ` = ?2 WHERE ` + c.column + ` = ?1`,
				`DELETE FROM ` + c.table + ` WHERE ` + c.column + ` = ?1`} {
				if _, err := tx.ExecContext(ctx, q, v, eventTime(t)); err != nil { //nolint:gosec // the names come from the table above
					return fmt.Errorf("rewriting %s.%s: %w", c.table, c.column, err)
				}
			}
		}
	}
	return nil
}

// v7ItemCategories rewrites the initial a stored item, item op or proposal names from version 6's next state
// to its category.
func v7ItemCategories(ctx context.Context, tx *sql.Tx) error {
	for _, c := range []struct{ table, column string }{{"items", "body"}, {"item_ops", "op"}, {"item_proposals", "op"}} {
		q := `UPDATE ` + c.table + ` SET ` + c.column + ` = replace(` + c.column + `, '"initial":"next"', '"initial":"ready"') WHERE instr(` + c.column + `, '"initial":"next"') > 0` //nolint:gosec // the names come from the list above
		if _, err := tx.ExecContext(ctx, q); err != nil {
			return fmt.Errorf("the initial of %s: %w", c.table, err)
		}
	}
	return nil
}

// v7AutoStars marks each star the loop set in version 6 with the autostar property, so the workflow that
// takes a stale auto-star off reads it as one.
func v7AutoStars(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO task_props (task_id, name, value, writer, at)
		SELECT task_id, 'autostar', '{"text":"on"}', 'workflow', at FROM task_props
		WHERE name = 'focus' AND writer = 'workflow' AND json_extract(value, '$.text') = 'on'
		ON CONFLICT (task_id, name) DO NOTHING`)
	return err
}

// v6Waiting is tasks.waiting of version 6.
type v6Waiting struct {
	On        []json.RawMessage
	LastNudge time.Time
	Resurface time.Time
}

// v7WaitingProps writes what a task's wait held as its properties: when the loop last nudged, when to check on
// it, which the person set, and, for a wait typed by hand, its owner. A derived wait's owers are its items'.
func v7WaitingProps(ctx context.Context, tx *sql.Tx) error {
	var ps []v7Prop
	err := eachRow(ctx, tx, `SELECT id, origin, waiting, updated FROM tasks WHERE waiting != '' ORDER BY id`, nil, func(r *sql.Rows) error {
		var id, origin, raw, updated string
		if err := r.Scan(&id, &origin, &raw, &updated); err != nil {
			return err
		}
		var w v6Waiting
		if err := json.Unmarshal([]byte(raw), &w); err != nil {
			return fmt.Errorf("the wait of %s: %w", id, err)
		}
		if !w.LastNudge.IsZero() {
			ps = append(ps, v7Prop{id, "last_nudge", map[string]time.Time{"time": w.LastNudge}, "workflow", updated})
		}
		if !w.Resurface.IsZero() {
			ps = append(ps, v7Prop{id, "check_on", map[string]time.Time{"time": w.Resurface}, "person", updated})
		}
		if origin != "derived" && len(w.On) > 0 {
			ps = append(ps, v7Prop{id, "owner", map[string][]json.RawMessage{"actors": w.On[:1]}, "person", updated})
		}
		return nil
	})
	if err != nil {
		return err
	}
	return putV7Props(ctx, tx, ps)
}

// v7HeldItem is an item a version 6 waiting task grouped.
type v7HeldItem struct {
	provider, key, item, summary string
	created                      time.Time
}

// v7SplitWaiting gives each item a waiting task grouped a task of its own, a copy of the waiting task with the
// item's summary as its title. The oldest item keeps the task, and with it every row keyed by its id; the
// others take the id reconcile derives for them, unless a task has it already, and an alias from the old id.
func v7SplitWaiting(ctx context.Context, tx *sql.Tx) error {
	var waits []string
	err := eachRow(ctx, tx, `SELECT id FROM tasks WHERE kind = 'waiting' ORDER BY id`, nil, func(r *sql.Rows) error {
		var id string
		if err := r.Scan(&id); err != nil {
			return err
		}
		waits = append(waits, id)
		return nil
	})
	if err != nil {
		return err
	}
	cols, err := v7TaskColumns(ctx, tx)
	if err != nil {
		return err
	}
	for _, old := range waits {
		held, err := v7HeldItems(ctx, tx, old)
		if err != nil {
			return err
		}
		if len(held) == 0 {
			continue
		}
		if err := v7Retitle(ctx, tx, old, held[0]); err != nil {
			return err
		}
		for _, it := range held[1:] {
			id, err := v7SplitID(ctx, tx, old, it)
			if err != nil {
				return err
			}
			for _, q := range []string{
				`INSERT INTO tasks (id, ` + cols + `) SELECT ?2, ` + cols + ` FROM tasks WHERE id = ?1`,
				`INSERT INTO task_units (task_id, provider, unit_key, ord) SELECT ?2, provider, unit_key, ord FROM task_units WHERE task_id = ?1`,
				`INSERT INTO task_props (task_id, name, value, writer, at) SELECT ?2, name, value, writer, at FROM task_props WHERE task_id = ?1`,
				`INSERT INTO task_aliases (old_id, new_id) VALUES (?1, ?2)`,
			} {
				if _, err := tx.ExecContext(ctx, q, old, id); err != nil {
					return fmt.Errorf("splitting %s: %w", old, err)
				}
			}
			if _, err := tx.ExecContext(ctx, `UPDATE task_items SET task_id = ? WHERE task_id = ? AND provider = ? AND unit_key = ? AND item = ?`,
				id, old, it.provider, it.key, it.item); err != nil {
				return fmt.Errorf("splitting %s: %w", old, err)
			}
			if err := v7Retitle(ctx, tx, id, it); err != nil {
				return err
			}
		}
	}
	return nil
}

// v7TaskColumns lists the columns of tasks but its id, as the live schema has them.
func v7TaskColumns(ctx context.Context, tx *sql.Tx) (string, error) {
	var cols []string
	err := eachRow(ctx, tx, `SELECT name FROM pragma_table_info('tasks') WHERE name != 'id' ORDER BY cid`, nil, func(r *sql.Rows) error {
		var c string
		if err := r.Scan(&c); err != nil {
			return err
		}
		cols = append(cols, c)
		return nil
	})
	return strings.Join(cols, ", "), err
}

// v7HeldItems are the items task id holds, oldest first; an item with no stored row last.
func v7HeldItems(ctx context.Context, tx *sql.Tx, id string) ([]v7HeldItem, error) {
	var out []v7HeldItem
	err := eachRow(ctx, tx, `SELECT h.provider, h.unit_key, h.item, coalesce(i.created_at, ''), coalesce(json_extract(i.body, '$.summary'), '')
		FROM task_items h LEFT JOIN items i ON i.provider = h.provider AND i.unit_key = h.unit_key AND i.id = h.item
		WHERE h.task_id = ? ORDER BY h.provider, h.unit_key, h.item`, []any{id}, func(r *sql.Rows) error {
		var it v7HeldItem
		var created string
		if err := r.Scan(&it.provider, &it.key, &it.item, &created, &it.summary); err != nil {
			return err
		}
		it.created = v6Time(created)
		out = append(out, it)
		return nil
	})
	slices.SortStableFunc(out, func(a, b v7HeldItem) int {
		if a.created.IsZero() != b.created.IsZero() {
			if a.created.IsZero() {
				return 1
			}
			return -1
		}
		return a.created.Compare(b.created)
	})
	return out, err
}

func v7Retitle(ctx context.Context, tx *sql.Tx, id string, it v7HeldItem) error {
	if it.summary == "" {
		return nil
	}
	_, err := tx.ExecContext(ctx, `UPDATE tasks SET title = ? WHERE id = ?`, it.summary, id)
	return err
}

// v7SplitID is the id of the task item it of waiting task old splits into: the id reconcile derives for the
// item, as version 6 hashed an action's, or one derived from old when a task has it already.
func v7SplitID(ctx context.Context, tx *sql.Tx, old string, it v7HeldItem) (string, error) {
	for _, seed := range []string{"action|", "split|" + old + "|"} {
		sum := sha256.Sum256([]byte(seed + it.provider + "|" + it.key + "|" + it.item))
		id := v7IDPrefix + hex.EncodeToString(sum[:])[:16]
		var n int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM tasks WHERE id = ?`, id).Scan(&n); err != nil {
			return "", err
		}
		if n == 0 {
			return id, nil
		}
	}
	return "", fmt.Errorf("splitting %s: both ids of item %s are taken", old, it.item)
}

// v7Ended is a close or message task of version 6 and the unit it was about.
type v7Ended struct {
	id, kind, category string
	provider, key      string
}

// v7EndCloseAndMessageTasks gives each close and message task's rows, its gates among them, to the task
// holding its unit, an open one first, and drops it, aliased to that task. A task that is open while no open
// task holds its unit stays, as the task the gates service opens for a gate on a unit no open task holds: a
// task of its unit, or a capture named after its message when it has none.
func v7EndCloseAndMessageTasks(ctx context.Context, tx *sql.Tx) error {
	var ended []v7Ended
	unitOf := func(col string) string {
		return `coalesce((SELECT ` + col + ` FROM task_units WHERE task_id = t.id ORDER BY ord LIMIT 1),
			(SELECT ` + col + ` FROM task_items WHERE task_id = t.id ORDER BY provider, unit_key, item LIMIT 1),
			(SELECT ` + col + ` FROM messages WHERE task_id = t.id ORDER BY id LIMIT 1), '')`
	}
	err := eachRow(ctx, tx, `SELECT t.id, t.kind, t.category, `+unitOf("provider")+`, `+unitOf("unit_key")+`
		FROM tasks t WHERE t.kind IN ('close', 'message') ORDER BY t.created, t.id`, nil, func(r *sql.Rows) error {
		var e v7Ended
		if err := r.Scan(&e.id, &e.kind, &e.category, &e.provider, &e.key); err != nil {
			return err
		}
		ended = append(ended, e)
		return nil
	})
	if err != nil {
		return err
	}
	for _, e := range ended {
		to, toOpen, err := v7UnitsTask(ctx, tx, e)
		if err != nil {
			return err
		}
		if to == "" || !toOpen && v7Open(e.category) {
			if err := v7KeepAsGateTask(ctx, tx, e); err != nil {
				return err
			}
			continue
		}
		if err := rekeyTask(ctx, tx, e.id, to); err != nil {
			return fmt.Errorf("moving %s task %s onto %s: %w", e.kind, e.id, to, err)
		}
		if err := dropTask(ctx, tx, e.id); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO task_aliases (old_id, new_id) VALUES (?, ?)`, e.id, to); err != nil {
			return err
		}
	}
	return nil
}

func v7Open(category string) bool { return category != "done" && category != "dropped" }

// v7UnitsTask is the task holding e's unit that e's rows go to, an open one first, then the oldest, and
// whether it is open; "" when none does. A close or message task is never one.
func v7UnitsTask(ctx context.Context, tx *sql.Tx, e v7Ended) (string, bool, error) {
	if e.provider == "" {
		return "", false, nil
	}
	cond := `id IN (SELECT task_id FROM task_units WHERE provider = ?1 AND unit_key = ?2 UNION SELECT task_id FROM task_items WHERE provider = ?1 AND unit_key = ?2)`
	if e.provider == v7Task {
		cond = `id = ?2`
	}
	var id, category string
	err := tx.QueryRowContext(ctx, `SELECT id, category FROM tasks WHERE `+cond+` AND id != ?3 AND kind NOT IN ('close', 'message')
		ORDER BY category IN ('done', 'dropped'), created, id LIMIT 1`, e.provider, e.key, e.id).Scan(&id, &category)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	return id, v7Open(category), err
}

// v7KeepAsGateTask keeps e as the task of its gates, one the gates opened, named after its message when it is
// about no unit. With its kind cleared it is the task of its unit the close and message tasks after it go to.
func v7KeepAsGateTask(ctx context.Context, tx *sql.Tx, e v7Ended) error {
	if _, err := tx.ExecContext(ctx, `UPDATE tasks SET origin = 'gate', kind = '' WHERE id = ?`, e.id); err != nil {
		return err
	}
	if e.provider != "" {
		return nil
	}
	var text string
	err := tx.QueryRowContext(ctx, `SELECT text FROM messages WHERE task_id = ? ORDER BY id LIMIT 1`, e.id).Scan(&text)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE tasks SET title = ? WHERE id = ?`, cmp.Or(text, e.id), e.id)
	return err
}

// v7MissingKinds gives the kind property to every task without one: the kind of its unit's provider, as the
// bundled plugins name it, and capture for a task with no unit.
func v7MissingKinds(ctx context.Context, tx *sql.Tx) error {
	var ps []v7Prop
	err := eachRow(ctx, tx, `SELECT id, updated, CASE provider WHEN '' THEN 'capture' WHEN 'github' THEN 'pull-request' WHEN 'slack' THEN 'thread' ELSE '' END,
			iif(provider = '', 'workflow', 'plugin')
		FROM (SELECT t.id, t.updated, coalesce((SELECT provider FROM task_units WHERE task_id = t.id ORDER BY ord LIMIT 1),
			(SELECT provider FROM task_items WHERE task_id = t.id ORDER BY provider, unit_key, item LIMIT 1), '') AS provider
			FROM tasks t WHERE NOT EXISTS (SELECT 1 FROM task_props p WHERE p.task_id = t.id AND p.name = 'kind')) ORDER BY id`, nil,
		func(r *sql.Rows) error {
			var id, updated, value, writer string
			if err := r.Scan(&id, &updated, &value, &writer); err != nil {
				return err
			}
			if value != "" {
				ps = append(ps, v7Prop{id, "kind", map[string]string{"text": value}, writer, updated})
			}
			return nil
		})
	if err != nil {
		return err
	}
	return putV7Props(ctx, tx, ps)
}
