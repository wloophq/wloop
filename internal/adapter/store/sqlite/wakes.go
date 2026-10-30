package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/core/wakes"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/wake"
)

// WakeStore is the tasks' wakes.
type WakeStore struct{ db *DB }

var _ wakes.Store = WakeStore{}

// Wakes is the store of the tasks' wakes.
func (db *DB) Wakes() WakeStore { return WakeStore{db} }

const wakeColumns = `task_id, until, on_kinds, actors, cond, expires, return_state`

// Put sets w's task's wake, replacing the one it had.
func (s WakeStore) Put(ctx context.Context, w wake.Wake) error {
	on, err := json.Marshal(nonNilList(w.On))
	if err != nil {
		return err
	}
	actors, err := json.Marshal(nonNilList(w.Actors))
	if err != nil {
		return err
	}
	_, err = s.db.Write(ctx, func(tx *Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO wakes (`+wakeColumns+`) VALUES (?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT (task_id) DO UPDATE SET until = excluded.until, on_kinds = excluded.on_kinds, actors = excluded.actors,
			cond = excluded.cond, expires = excluded.expires, return_state = excluded.return_state`,
			string(w.Task), eventTime(w.Until), string(on), string(actors), w.When, eventTime(w.Expires), w.Return); err != nil {
			return fmt.Errorf("the wake of %s: %w", w.Task, err)
		}
		return touchWake(ctx, tx, w.Task)
	})
	return err
}

// Drop removes task id's wake.
func (s WakeStore) Drop(ctx context.Context, id task.ID) error {
	_, err := s.db.Write(ctx, func(tx *Tx) error { return dropWake(ctx, tx, id) })
	return err
}

func dropWake(ctx context.Context, tx *Tx, id task.ID) error {
	res, err := tx.ExecContext(ctx, `DELETE FROM wakes WHERE task_id = ?`, string(id))
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil || n == 0 {
		return err
	}
	return touchWake(ctx, tx, id)
}

// touchWake notes a change of task id's wake on the task and on the units it holds, whose rows its mute
// overlays.
func touchWake(ctx context.Context, tx *Tx, id task.ID) error {
	rows, err := tx.QueryContext(ctx, `SELECT provider, unit_key FROM task_units WHERE task_id = ?1
		UNION SELECT provider, unit_key FROM task_items WHERE task_id = ?1`, string(id))
	if err != nil {
		return err
	}
	defer rows.Close()
	units := []unit.Unit{task.UnitOf(id)}
	for rows.Next() {
		var u unit.Unit
		var p string
		if err := rows.Scan(&p, &u.Key); err != nil {
			return err
		}
		u.Provider = unit.ProviderID(p)
		units = append(units, u)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	tx.Touch(port.TopicWakes, units...)
	return nil
}

// Of is task id's wake, false when it has none.
func (s WakeStore) Of(ctx context.Context, id task.ID) (wake.Wake, bool, error) {
	ws, err := s.query(ctx, `SELECT `+wakeColumns+` FROM wakes WHERE task_id = ?`, string(id))
	if err != nil || len(ws) == 0 {
		return wake.Wake{}, false, err
	}
	return ws[0], true, nil
}

// Due are the wakes whose until or expires is set and not after now.
func (s WakeStore) Due(ctx context.Context, now time.Time) ([]wake.Wake, error) {
	at := eventTime(now)
	return s.query(ctx, `SELECT `+wakeColumns+` FROM wakes WHERE until != '' AND until <= ? OR expires != '' AND expires <= ?
		ORDER BY task_id`, at, at)
}

// Watching are the wakes with on or a condition set.
func (s WakeStore) Watching(ctx context.Context) ([]wake.Wake, error) {
	return s.query(ctx, `SELECT `+wakeColumns+` FROM wakes WHERE on_kinds != '[]' OR cond != '' ORDER BY task_id`)
}

// UnitWakes are the wakes of the open tasks that hold units, by unit: the
// wake of a unit's oldest such task. A unit with none is absent.
func (s WakeStore) UnitWakes(ctx context.Context, units []unit.Unit) (map[unit.Unit]wake.Wake, error) {
	want := make(map[unit.Unit]bool, len(units))
	for _, u := range units {
		want[u] = true
	}
	out := map[unit.Unit]wake.Wake{}
	err := s.db.Read(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT x.provider, x.unit_key, w.task_id, w.until, w.on_kinds, w.actors, w.cond, w.expires,
			w.return_state FROM wakes w JOIN tasks t ON t.id = w.task_id
			JOIN (SELECT task_id, provider, unit_key FROM task_units UNION SELECT task_id, provider, unit_key FROM task_items) x
			ON x.task_id = w.task_id
			WHERE t.category NOT IN (?, ?) ORDER BY t.created, t.id`, string(task.CategoryDone), string(task.CategoryDropped))
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var p string
			var u unit.Unit
			w, err := scanWake(rows, &p, &u.Key)
			if err != nil {
				return err
			}
			u.Provider = unit.ProviderID(p)
			if _, seen := out[u]; want[u] && !seen {
				out[u] = w
			}
		}
		return rows.Err()
	})
	return out, err
}

func (s WakeStore) query(ctx context.Context, q string, args ...any) ([]wake.Wake, error) {
	var out []wake.Wake
	err := s.db.Read(ctx, func(tx *sql.Tx) error {
		var err error
		out, err = scanWakes(tx.QueryContext(ctx, q, args...))
		return err
	})
	return out, err
}

func scanWakes(rows *sql.Rows, err error) ([]wake.Wake, error) {
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []wake.Wake{}
	for rows.Next() {
		w, err := scanWake(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

// scanWake reads a row of wakeColumns, after the columns of before.
func scanWake(rows *sql.Rows, before ...any) (wake.Wake, error) {
	var w wake.Wake
	var id, until, on, actors, expires string
	if err := rows.Scan(append(before, &id, &until, &on, &actors, &w.When, &expires, &w.Return)...); err != nil {
		return wake.Wake{}, err
	}
	w.Task = task.ID(id)
	var err error
	if w.Until, err = taskTime(until); err != nil {
		return wake.Wake{}, fmt.Errorf("the wake of %s: %w", id, err)
	}
	if w.Expires, err = taskTime(expires); err != nil {
		return wake.Wake{}, fmt.Errorf("the wake of %s: %w", id, err)
	}
	if err := json.Unmarshal([]byte(on), &w.On); err != nil {
		return wake.Wake{}, fmt.Errorf("the wake of %s: %w", id, err)
	}
	if err := json.Unmarshal([]byte(actors), &w.Actors); err != nil {
		return wake.Wake{}, fmt.Errorf("the wake of %s: %w", id, err)
	}
	return w, nil
}

func nonNilList(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
