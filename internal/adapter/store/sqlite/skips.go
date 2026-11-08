package sqlite

import (
	"context"
	"database/sql"
	"time"

	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/core/skips"
	"github.com/wloophq/wloop/internal/domain/unit"
)

var _ skips.Store = (*DB)(nil)

// SaveSkip keeps that workflow is skipped on u; saving it again keeps the first time.
func (db *DB) SaveSkip(ctx context.Context, workflow string, u unit.Unit, at time.Time) error {
	_, err := db.Write(ctx, func(tx *Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO workflow_skips (provider, unit_key, workflow, at) VALUES (?, ?, ?, ?)
			ON CONFLICT DO NOTHING`, string(u.Provider), u.Key, workflow, at.UTC().Format(TimeLayout))
		tx.Touch(port.TopicOverrides, u)
		return err
	})
	return err
}

// DeleteSkip forgets that workflow is skipped on u; deleting none is no error.
func (db *DB) DeleteSkip(ctx context.Context, workflow string, u unit.Unit) error {
	_, err := db.Write(ctx, func(tx *Tx) error {
		_, err := tx.ExecContext(ctx, `DELETE FROM workflow_skips WHERE provider = ? AND unit_key = ? AND workflow = ?`,
			string(u.Provider), u.Key, workflow)
		tx.Touch(port.TopicOverrides, u)
		return err
	})
	return err
}

// AllSkips are the skipped workflows by unit, each unit's sorted by name.
func (db *DB) AllSkips(ctx context.Context) (map[unit.Unit][]string, error) {
	out := map[unit.Unit][]string{}
	err := eachRow(ctx, db.R(), `SELECT provider, unit_key, workflow FROM workflow_skips ORDER BY provider, unit_key, workflow`, nil,
		func(r *sql.Rows) error {
			var u unit.Unit
			var wf string
			if err := r.Scan(&u.Provider, &u.Key, &wf); err != nil {
				return err
			}
			out[u] = append(out[u], wf)
			return nil
		})
	return out, err
}
