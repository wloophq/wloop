package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/wloophq/wloop/internal/core/automation"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/workflow"
)

type events struct{ db *DB }

var _ automation.Events = events{}

// Events reads the events table by id for event workflows, and keeps each
// workflow's cursor and last firing. Advancing a cursor notes nothing.
func (db *DB) Events() automation.Events { return events{db} }

const (
	eventsSinceSQL      = `SELECT id, provider, unit_key, kind, key, actor, data, at FROM events WHERE id > ? ORDER BY id`
	eventsSinceKindsSQL = `SELECT id, provider, unit_key, kind, key, actor, data, at FROM events
		WHERE id > ? AND +kind IN (SELECT value FROM json_each(?)) ORDER BY id`
)

func (e events) Since(ctx context.Context, cursor int64, kinds []string) ([]workflow.Event, error) {
	q, args := eventsSinceSQL, []any{cursor}
	if len(kinds) > 0 {
		list, err := json.Marshal(kinds)
		if err != nil {
			return nil, err
		}
		q, args = eventsSinceKindsSQL, append(args, string(list))
	}
	rows, err := e.db.R().QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []workflow.Event
	for rows.Next() {
		var ev workflow.Event
		var provider, at string
		if err := rows.Scan(&ev.ID, &provider, &ev.Unit.Key, &ev.Kind, &ev.Key, &ev.Actor, &ev.Data, &at); err != nil {
			return nil, err
		}
		ev.Unit.Provider = unit.ProviderID(provider)
		ev.Data, ev.Chain = workflow.SplitChain(ev.Data)
		if ev.At, err = parseStoredTime(at); err != nil {
			return nil, fmt.Errorf("event %d: %w", ev.ID, err)
		}
		out = append(out, ev)
	}
	return out, rows.Err()
}

func (e events) Head(ctx context.Context) (int64, error) {
	var head int64
	err := e.db.R().QueryRowContext(ctx, `SELECT coalesce(max(id), 0) FROM events`).Scan(&head)
	return head, err
}

func (e events) Cursor(ctx context.Context, wf string) (int64, time.Time, bool, error) {
	var cursor int64
	var last string
	err := e.db.R().QueryRowContext(ctx, `SELECT cursor, last_fire FROM workflow_cursors WHERE workflow = ?`, wf).Scan(&cursor, &last)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, time.Time{}, false, nil
	}
	if err != nil {
		return 0, time.Time{}, false, err
	}
	lastFire, err := parseStoredTime(last)
	if err != nil {
		return 0, time.Time{}, false, fmt.Errorf("the last firing of %s: %w", wf, err)
	}
	return cursor, lastFire, true, nil
}

func (e events) Advance(ctx context.Context, wf string, cursor int64, lastFire time.Time) error {
	_, err := e.db.Write(ctx, func(tx *Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO workflow_cursors (workflow, cursor, last_fire) VALUES (?, ?, ?)
			ON CONFLICT (workflow) DO UPDATE SET cursor = excluded.cursor, last_fire = excluded.last_fire`,
			wf, cursor, eventTime(lastFire))
		return err
	})
	return err
}
