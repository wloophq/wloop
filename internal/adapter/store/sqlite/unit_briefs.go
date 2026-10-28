package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/domain/unit"
)

const (
	// pluginBriefRunSQL is the unit's plugin brief run of the day of at,
	// which a later brief that day updates in place.
	pluginBriefRunSQL = `SELECT r.id FROM unit_briefs b JOIN brief_runs r ON r.id = b.last_run
		WHERE b.provider = ? AND b.unit_key = ? AND r.trigger = 'plugin' AND substr(r.started_at, 1, 10) = substr(?, 1, 10)`
	updatePluginBriefSQL = `UPDATE brief_runs SET version = ?, brief = json(?), ended_at = ? WHERE id = ?`
	insertPluginBriefSQL = `INSERT INTO brief_runs (provider, unit_key, intent, trigger, version, attempt_of, started_at, ended_at, outcome, brief)
		VALUES (?, ?, 'brief', 'plugin', ?, '', ?, ?, 'ok', json(?))`
	usePluginBriefSQL = `INSERT INTO unit_briefs (provider, unit_key, run, last_run) VALUES (?, ?, ?, ?)
		ON CONFLICT (provider, unit_key) DO UPDATE SET run = excluded.run, last_run = excluded.last_run`
)

// PutUnitBrief keeps body, a brief's JSON a source plugin wrote of u at
// version, as the brief u uses: u's plugin run of at's day, updated in
// place, or a new ok run.
func (db *DB) PutUnitBrief(ctx context.Context, u unit.Unit, version, body string, at time.Time) error {
	_, err := db.Write(ctx, func(tx *Tx) error {
		tx.Touch(port.TopicBriefs, u)
		ts := formatTime(at)
		var id int64
		err := tx.QueryRowContext(ctx, pluginBriefRunSQL, string(u.Provider), u.Key, ts).Scan(&id)
		switch {
		case err == nil:
			_, err = tx.ExecContext(ctx, updatePluginBriefSQL, version, body, ts, id)
			return err
		case !errors.Is(err, sql.ErrNoRows):
			return err
		}
		res, err := tx.ExecContext(ctx, insertPluginBriefSQL, string(u.Provider), u.Key, version, ts, ts, body)
		if err != nil {
			return err
		}
		id, err = res.LastInsertId()
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, usePluginBriefSQL, string(u.Provider), u.Key, id, id)
		return err
	})
	return err
}
