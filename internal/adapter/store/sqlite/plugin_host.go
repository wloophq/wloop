package sqlite

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/domain/event"
	"github.com/wloophq/wloop/internal/domain/unit"
)

const (
	insertSourceEventSQL = `INSERT OR IGNORE INTO events (provider, unit_key, kind, key, actor, at, data) VALUES (?, ?, ?, ?, ?, ?, ?)`
	fillSourceEventSQL   = `UPDATE events SET actor = iif(actor = '', ?1, actor), data = iif(data = '', ?2, data)
		WHERE provider = ?3 AND unit_key = ?4 AND kind = ?5 AND key = '' AND at = ?6 AND ((actor = '' AND ?1 != '') OR (data = '' AND ?2 != ''))`
	unitEventsSQL = `SELECT provider, unit_key, kind, key, actor, at, data FROM events
		WHERE (provider, unit_key) IN (SELECT value->>0, value->>1 FROM json_each(?1))
		AND (?2 = '[]' OR kind IN (SELECT value FROM json_each(?2))) AND at >= ?3 ORDER BY at, id`
	rehomeDraftSQL = `UPDATE drafts SET unit_key = ?, rev = rev + 1 WHERE provider = ? AND unit_key = ?`
	// rehomeDraftGatesSQL moves the gates of the drafts it moves along, so a draft's gate stays on its unit.
	rehomeDraftGatesSQL = `UPDATE gates SET unit_key = ? WHERE kind = 'draft' AND provider = ? AND unit_key = ?`
)

// RecordSourceEvents stores the events a source plugin observed on its
// units: a keyed one at most once per unit, kind and key, an unkeyed one at
// most once per unit, kind and time, where the stored one gains the actor
// and the data it lacks. It notes TopicSourceEvents on the units it changed.
func (s *Store) RecordSourceEvents(ctx context.Context, evs ...port.Event) error {
	for _, ev := range evs {
		if ev.At.IsZero() {
			return fmt.Errorf("the %s event of %s has no time", ev.Kind, ev.Unit)
		}
	}
	_, err := s.db.Write(ctx, func(tx *Tx) error {
		for _, ev := range evs {
			at := ev.At.UTC().Format(event.TimeLayout)
			res, err := tx.ExecContext(ctx, insertSourceEventSQL, string(ev.Unit.Provider), ev.Unit.Key, ev.Kind, ev.Key, ev.Actor, at, ev.Data)
			if err != nil {
				return err
			}
			n, err := res.RowsAffected()
			if err != nil {
				return err
			}
			if n == 0 && ev.Key == "" {
				if res, err = tx.ExecContext(ctx, fillSourceEventSQL, ev.Actor, ev.Data, string(ev.Unit.Provider), ev.Unit.Key, ev.Kind, at); err != nil {
					return err
				}
				if n, err = res.RowsAffected(); err != nil {
					return err
				}
			}
			if n > 0 {
				tx.Touch(port.TopicSourceEvents, ev.Unit)
			}
		}
		return nil
	})
	return err
}

// UnitEvents are the events of units of kinds, every kind when kinds is
// empty, at or after since, oldest first.
func (s *Store) UnitEvents(ctx context.Context, units []unit.Unit, kinds []string, since time.Time) ([]port.Event, error) {
	if len(units) == 0 {
		return nil, nil
	}
	pairs := make([][2]string, len(units))
	for i, u := range units {
		pairs[i] = [2]string{string(u.Provider), u.Key}
	}
	ul, err := json.Marshal(pairs)
	if err != nil {
		return nil, err
	}
	if kinds == nil {
		kinds = []string{}
	}
	kl, err := json.Marshal(kinds)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.R().QueryContext(ctx, unitEventsSQL, string(ul), string(kl), since.UTC().Format(event.TimeLayout))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []port.Event
	for rows.Next() {
		var ev port.Event
		var provider, at string
		if err := rows.Scan(&provider, &ev.Unit.Key, &ev.Kind, &ev.Key, &ev.Actor, &at, &ev.Data); err != nil {
			return nil, err
		}
		ev.Unit.Provider = unit.ProviderID(provider)
		if ev.At, err = time.Parse(event.TimeLayout, at); err != nil {
			return nil, err
		}
		out = append(out, ev)
	}
	return out, rows.Err()
}

// SetMarks sets u's marks by name in one transaction; a value "" removes
// one.
func (db *DB) SetMarks(ctx context.Context, u unit.Unit, marks map[string]string) error {
	_, err := db.Write(ctx, func(tx *Tx) error {
		for name, value := range marks {
			if err := saveMark(ctx, tx, u, name, value); err != nil {
				return err
			}
		}
		return nil
	})
	return err
}

// RehomeDrafts moves the drafts of each unit of moves onto the unit it
// maps to, in one transaction, and counts them; a second call moves none.
func (db *DB) RehomeDrafts(ctx context.Context, moves map[unit.Unit]unit.Unit) (int, error) {
	moved := 0
	_, err := db.Write(ctx, func(tx *Tx) error {
		moved = 0
		for from, to := range moves {
			if from == to {
				continue
			}
			if from.Provider != to.Provider {
				return fmt.Errorf("drafts of %s cannot move to %s, a unit of another provider", from, to)
			}
			res, err := tx.ExecContext(ctx, rehomeDraftSQL, to.Key, string(from.Provider), from.Key)
			if err != nil {
				return err
			}
			n, err := res.RowsAffected()
			if err != nil {
				return err
			}
			if n > 0 {
				moved += int(n)
				tx.Touch(port.TopicDrafts, to)
			}
			res, err = tx.ExecContext(ctx, rehomeDraftGatesSQL, to.Key, string(from.Provider), from.Key)
			if err != nil {
				return err
			}
			if n, err := res.RowsAffected(); err != nil || n > 0 {
				if err != nil {
					return err
				}
				tx.Touch(port.TopicGates, to)
			}
		}
		return nil
	})
	return moved, err
}
