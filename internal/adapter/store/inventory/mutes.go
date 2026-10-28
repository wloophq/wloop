package inventory

import (
	"context"
	"time"

	"github.com/wloophq/wloop/internal/domain/unit"
)

// UnitMute is a unit's mute as its override holds it.
type UnitMute struct {
	Until            time.Time
	UntilStateChange bool
	Kind, WaitingOn  string
}

// UnitMutes are the mutes the overrides hold of units, of any provider; a
// unit with none is absent.
func (s *Store) UnitMutes(ctx context.Context, units []unit.Unit) (map[unit.Unit]UnitMute, error) {
	want := make(map[unit.Unit]bool, len(units))
	for _, u := range units {
		want[u] = true
	}
	rows, err := s.db.R().QueryContext(ctx, `SELECT provider, unit_key, muted_until, mute_until_state, muted_kind, muted_waiting_on
		FROM overrides WHERE muted_until != '' OR mute_until_state != 0`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[unit.Unit]UnitMute{}
	for rows.Next() {
		var provider, key, until string
		var m UnitMute
		if err := rows.Scan(&provider, &key, &until, &m.UntilStateChange, &m.Kind, &m.WaitingOn); err != nil {
			return nil, err
		}
		u := unit.Unit{Provider: unit.ProviderID(provider), Key: key}
		if !want[u] {
			continue
		}
		if m.Until, err = parseTS(until); err != nil {
			return nil, err
		}
		out[u] = m
	}
	return out, rows.Err()
}
