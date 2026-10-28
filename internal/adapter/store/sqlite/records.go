package sqlite

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/core/timeline"
	"github.com/wloophq/wloop/internal/domain/event"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
)

var (
	_ timeline.Log      = (*DB)(nil)
	_ timeline.Records  = (*DB)(nil)
	_ timeline.Holdings = (*DB)(nil)
)

// unitsIn is the (provider, unit_key) IN clause of n units.
func unitsIn(n int) string {
	return `(provider, unit_key) IN (VALUES (?, ?)` + strings.Repeat(", (?, ?)", n-1) + `)`
}

func unitArgs(units []unit.Unit) []any {
	out := make([]any, 0, 2*len(units))
	for _, u := range units {
		out = append(out, string(u.Provider), u.Key)
	}
	return out
}

// EventsOf are the events of units f keeps, newest first by their row id.
func (db *DB) EventsOf(ctx context.Context, units []unit.Unit, f timeline.Filter) ([]timeline.Event, error) {
	if len(units) == 0 {
		return []timeline.Event{}, nil
	}
	q := `SELECT id, provider, unit_key, kind, key, actor, at, data FROM events WHERE ` + unitsIn(len(units))
	args := unitArgs(units)
	if len(f.Kinds) > 0 {
		q += ` AND kind IN (?` + strings.Repeat(", ?", len(f.Kinds)-1) + `)`
		for _, k := range f.Kinds {
			args = append(args, k)
		}
	}
	if f.Before > 0 {
		q += ` AND id < ?`
		args = append(args, f.Before)
	}
	q += ` ORDER BY id DESC`
	if f.Limit > 0 {
		q += ` LIMIT ?`
		args = append(args, f.Limit)
	}
	out := []timeline.Event{}
	err := eachRow(ctx, db.R(), q, args, func(rows *sql.Rows) error {
		var ev timeline.Event
		var provider, at string
		if err := rows.Scan(&ev.Seq, &provider, &ev.Unit.Key, &ev.Kind, &ev.Key, &ev.Actor, &at, &ev.Data); err != nil {
			return err
		}
		ev.Unit.Provider = unit.ProviderID(provider)
		t, err := time.Parse(event.TimeLayout, at)
		if err != nil {
			return err
		}
		ev.At = t
		out = append(out, ev)
		return nil
	})
	return out, err
}

// Records are the record of kind each of units keeps; with no units, every unit's.
func (db *DB) Records(ctx context.Context, kind string, units ...unit.Unit) (map[unit.Unit]port.Event, error) {
	out := map[unit.Unit]port.Event{}
	if len(units) == 0 {
		return out, db.everyRecord(ctx, kind, out)
	}
	evs, err := db.EventsOf(ctx, units, timeline.Filter{Kinds: []string{timeline.KindRecord}})
	if err != nil {
		return nil, err
	}
	for _, ev := range evs {
		if _, ok := out[ev.Unit]; !ok && ev.Key == kind {
			out[ev.Unit] = ev.Event
		}
	}
	return out, nil
}

// UnitsOf are the units task id holds, in its order.
func (db *DB) UnitsOf(ctx context.Context, id task.ID) ([]unit.Unit, error) {
	var out []unit.Unit
	err := eachRow(ctx, db.R(), `SELECT provider, unit_key FROM task_units WHERE task_id = ? ORDER BY ord`, []any{string(id)}, func(rows *sql.Rows) error {
		var u unit.Unit
		var provider string
		if err := rows.Scan(&provider, &u.Key); err != nil {
			return err
		}
		u.Provider = unit.ProviderID(provider)
		out = append(out, u)
		return nil
	})
	return out, err
}

// PutRecord keeps data as u's record of kind, written by w at at, in place
// of the one it kept: the new record takes the newest Seq.
func (db *DB) PutRecord(ctx context.Context, u unit.Unit, kind string, w task.Writer, data []byte, at time.Time) error {
	_, err := db.Write(ctx, func(tx *Tx) error {
		return tx.PutRecord(ctx, u, kind, string(w), string(data), at)
	})
	return err
}

// PutRecord is DB.PutRecord in tx.
func (tx *Tx) PutRecord(ctx context.Context, u unit.Unit, kind, actor, data string, at time.Time) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM events WHERE provider = ? AND unit_key = ? AND kind = ? AND key = ?`,
		string(u.Provider), u.Key, timeline.KindRecord, kind); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO events (provider, unit_key, kind, key, actor, at, data) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		string(u.Provider), u.Key, timeline.KindRecord, kind, actor, at.UTC().Format(event.TimeLayout), data); err != nil {
		return err
	}
	tx.Touch(port.TopicLoopEvents, u)
	return nil
}

// DropRecord forgets u's record of kind; dropped says it kept one.
func (tx *Tx) DropRecord(ctx context.Context, u unit.Unit, kind string) (dropped bool, err error) {
	res, err := tx.ExecContext(ctx, `DELETE FROM events WHERE provider = ? AND unit_key = ? AND kind = ? AND key = ?`,
		string(u.Provider), u.Key, timeline.KindRecord, kind)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil || n == 0 {
		return false, err
	}
	tx.Touch(port.TopicLoopEvents, u)
	return true, nil
}

// TasksHolding are the tasks that hold u, oldest first.
func (db *DB) TasksHolding(ctx context.Context, u unit.Unit) ([]task.ID, error) {
	var out []task.ID
	err := eachRow(ctx, db.R(), `SELECT task_id FROM task_units WHERE provider = ? AND unit_key = ? ORDER BY task_id`,
		[]any{string(u.Provider), u.Key}, func(rows *sql.Rows) error {
			var id string
			if err := rows.Scan(&id); err != nil {
				return err
			}
			out = append(out, task.ID(id))
			return nil
		})
	return out, err
}

// ItemsHeld are the items of u each task holding u or one of its items holds; a task that holds u and none
// of its items maps to none.
func (db *DB) ItemsHeld(ctx context.Context, u unit.Unit) (map[task.ID][]string, error) {
	out := map[task.ID][]string{}
	err := eachRow(ctx, db.R(), `SELECT task_id, '' FROM task_units WHERE provider = ?1 AND unit_key = ?2
		UNION ALL SELECT task_id, item FROM task_items WHERE provider = ?1 AND unit_key = ?2 ORDER BY 1, 2`,
		[]any{string(u.Provider), u.Key}, func(rows *sql.Rows) error {
			var id, item string
			if err := rows.Scan(&id, &item); err != nil {
				return err
			}
			if _, ok := out[task.ID(id)]; !ok {
				out[task.ID(id)] = []string{}
			}
			if item != "" {
				out[task.ID(id)] = append(out[task.ID(id)], item)
			}
			return nil
		})
	return out, err
}

// everyRecord puts in out every unit's record of kind.
func (db *DB) everyRecord(ctx context.Context, kind string, out map[unit.Unit]port.Event) error {
	return eachRow(ctx, db.R(), `SELECT provider, unit_key, actor, at, data FROM events WHERE kind = ? AND key = ?`,
		[]any{timeline.KindRecord, kind}, func(rows *sql.Rows) error {
			ev := port.Event{Kind: timeline.KindRecord, Key: kind}
			var provider, at string
			if err := rows.Scan(&provider, &ev.Unit.Key, &ev.Actor, &at, &ev.Data); err != nil {
				return err
			}
			ev.Unit.Provider = unit.ProviderID(provider)
			t, err := time.Parse(event.TimeLayout, at)
			if err != nil {
				return err
			}
			ev.At = t
			out[ev.Unit] = ev
			return nil
		})
}
