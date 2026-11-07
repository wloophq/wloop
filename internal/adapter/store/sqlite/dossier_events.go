package sqlite

import (
	"context"
	"strings"
	"time"

	"github.com/wloophq/wloop/internal/domain/event"
	"github.com/wloophq/wloop/internal/domain/unit"
)

// CallSpent is the cost of the calls recorded in [from, to) as events of kinds, whose Data is the cost.
func (db *DB) CallSpent(ctx context.Context, from, to time.Time, kinds ...string) (float64, error) {
	if len(kinds) == 0 {
		return 0, nil
	}
	args := []any{from.UTC().Format(event.TimeLayout), to.UTC().Format(event.TimeLayout)}
	for _, k := range kinds {
		args = append(args, k)
	}
	var spent float64
	err := db.R().QueryRowContext(ctx, `SELECT COALESCE(SUM(CAST(data AS REAL)), 0) FROM events
		WHERE at >= ? AND at < ? AND kind IN (?`+strings.Repeat(",?", len(kinds)-1)+`)`, args...).Scan(&spent)
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
