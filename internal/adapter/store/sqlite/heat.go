package sqlite

import (
	"context"
	"encoding/json"
	"time"

	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/domain/unit"
)

// DefaultWarmFor is items.heat.warm_for when unset.
const DefaultWarmFor = 72 * time.Hour

// HeatReader reads port.Heat from the items ledger: active while a unit has an open item,
// warm for WarmFor after its last item resolved, cold otherwise.
type HeatReader struct {
	DB      *DB
	WarmFor func() time.Duration // nil or zero: DefaultWarmFor
	Clock   port.Clock
}

var _ port.HeatReader = HeatReader{}

const heatSQL = `SELECT json_extract(u.value, '$[0]'), json_extract(u.value, '$[1]'),
	MAX(i.state = 'open'), MAX(i.state = 'resolved' AND julianday(i.updated_at) > julianday(?))
	FROM json_each(?) u JOIN items i ON i.provider = json_extract(u.value, '$[0]') AND i.unit_key = json_extract(u.value, '$[1]')
	GROUP BY 1, 2`

func (h HeatReader) Heat(ctx context.Context, units []unit.Unit) (map[unit.Unit]port.Heat, error) {
	out := map[unit.Unit]port.Heat{}
	if len(units) == 0 {
		return out, nil
	}
	pairs := make([][2]string, len(units))
	for i, u := range units {
		pairs[i] = [2]string{string(u.Provider), u.Key}
	}
	list, err := json.Marshal(pairs)
	if err != nil {
		return nil, err
	}
	warmFor := DefaultWarmFor
	if h.WarmFor != nil && h.WarmFor() > 0 {
		warmFor = h.WarmFor()
	}
	cutoff := formatTime(h.Clock.Now().Add(-warmFor))
	rows, err := h.DB.R().QueryContext(ctx, heatSQL, cutoff, string(list))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var provider, key string
		var open, warm bool
		if err := rows.Scan(&provider, &key, &open, &warm); err != nil {
			return nil, err
		}
		u := unit.Unit{Provider: unit.ProviderID(provider), Key: key}
		switch {
		case open:
			out[u] = port.HeatActive
		case warm:
			out[u] = port.HeatWarm
		}
	}
	return out, rows.Err()
}
