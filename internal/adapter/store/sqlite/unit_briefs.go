package sqlite

import (
	"context"
	"time"

	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/domain/unit"
)

const (
	insertPluginBriefSQL = `INSERT INTO brief_runs (provider, unit_key, intent, trigger, version, attempt_of, started_at, ended_at, outcome, brief)
		VALUES (?, ?, 'brief', 'plugin', ?, '', ?, ?, 'ok', json(?))`
	usePluginBriefSQL = `INSERT INTO unit_briefs (provider, unit_key, run, last_run) VALUES (?, ?, ?, ?)
		ON CONFLICT (provider, unit_key) DO UPDATE SET run = excluded.run, last_run = excluded.last_run`
)

// PutUnitBrief keeps body, a brief's JSON a source plugin wrote of u at
// version, as an ok run at at and the brief u uses.
func (db *DB) PutUnitBrief(ctx context.Context, u unit.Unit, version, body string, at time.Time) error {
	_, err := db.Write(ctx, func(tx *Tx) error {
		tx.Touch(port.TopicBriefs, u)
		ts := formatTime(at)
		res, err := tx.ExecContext(ctx, insertPluginBriefSQL, string(u.Provider), u.Key, version, ts, ts, body)
		if err != nil {
			return err
		}
		id, err := res.LastInsertId()
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, usePluginBriefSQL, string(u.Provider), u.Key, id, id)
		return err
	})
	return err
}
