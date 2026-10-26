package sqlite

import (
	"context"

	"github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
)

// SQLite takes the bare intent from the row of max(seq).
const activitiesSQL = `SELECT unit_key, 'turns', count(*), sum(state IN ('sent', 'started', 'asked')), intent, max(seq), 0.0, ''
	FROM turns WHERE provider = ?1 GROUP BY unit_key
	UNION ALL
	SELECT t.unit_key, 'usage', 0, 0, '', sum(u.input_tokens + u.output_tokens), total(u.cost_usd), ''
	FROM turn_usage u JOIN turns t ON t.seq = u.turn_seq WHERE t.provider = ?1 GROUP BY t.unit_key
	UNION ALL
	SELECT unit_key, 'brief', 0, 0, '', 0, 0.0, retry_at FROM unit_briefs WHERE provider = ?1 AND retry_at != ''`

// Activities reads the activity of every unit of provider that has any, in
// one statement: one aggregated part per table.
func (db *DB) Activities(ctx context.Context, provider unit.ProviderID) (map[unit.Unit]turn.Activity, error) {
	rows, err := db.R().QueryContext(ctx, activitiesSQL, string(provider))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[unit.Unit]turn.Activity{}
	for rows.Next() {
		var key, part, intent, retry string
		var turns, open int
		var n int64
		var cost float64
		if err := rows.Scan(&key, &part, &turns, &open, &intent, &n, &cost, &retry); err != nil {
			return nil, err
		}
		u := unit.Unit{Provider: provider, Key: key}
		a := out[u]
		switch part {
		case "turns":
			a.Turns, a.OpenTurns, a.LastIntent = turns, open, intent
		case "usage":
			a.Tokens, a.CostUSD = n, cost
		case "brief":
			a.BriefRetryAt = parseTime(retry)
		}
		out[u] = a
	}
	return out, rows.Err()
}
