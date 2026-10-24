package sqlite

import (
	"context"
	"time"

	"github.com/wloophq/wloop/internal/core/dossiers"
	"github.com/wloophq/wloop/internal/domain/event"
	"github.com/wloophq/wloop/internal/domain/unit"
)

// DossierSpent is the cost of the dossier calls recorded in [from, to).
func (db *DB) DossierSpent(ctx context.Context, from, to time.Time) (float64, error) {
	var spent float64
	err := db.R().QueryRowContext(ctx, `SELECT COALESCE(SUM(CAST(data AS REAL)), 0) FROM events
		WHERE kind = ? AND at >= ? AND at < ?`,
		dossiers.KindCall, from.UTC().Format(event.TimeLayout), to.UTC().Format(event.TimeLayout)).Scan(&spent)
	return spent, err
}

// UnitEventKeys maps the keys of u's events of kind to when each was recorded.
func (db *DB) UnitEventKeys(ctx context.Context, u unit.Unit, kind string) (map[string]time.Time, error) {
	rows, err := db.R().QueryContext(ctx, `SELECT key, at FROM events WHERE provider = ? AND unit_key = ? AND kind = ? AND key != ''`,
		string(u.Provider), u.Key, kind)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]time.Time{}
	for rows.Next() {
		var key, at string
		if err := rows.Scan(&key, &at); err != nil {
			return nil, err
		}
		t, err := time.Parse(event.TimeLayout, at)
		if err != nil {
			return nil, err
		}
		out[key] = t
	}
	return out, rows.Err()
}
