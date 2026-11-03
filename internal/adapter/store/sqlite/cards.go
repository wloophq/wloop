package sqlite

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/domain/card"
	"github.com/wloophq/wloop/internal/domain/unit"
)

type cardStore struct{ db *DB }

var _ card.Store = cardStore{}

// Cards is the store of the units' card snapshots.
func (db *DB) Cards() card.Store { return cardStore{db} }

const (
	cardsOfUnitsSQL = `SELECT provider, unit_key, card, at FROM unit_cards
		WHERE (provider, unit_key) IN (SELECT value->>0, value->>1 FROM json_each(?))`
	cardBodySQL   = `SELECT card FROM unit_cards WHERE provider = ? AND unit_key = ?`
	upsertCardSQL = `INSERT INTO unit_cards (provider, unit_key, card, at) VALUES (?, ?, ?, ?)
		ON CONFLICT (provider, unit_key) DO UPDATE SET card = excluded.card, at = excluded.at`
)

// Save stores c as its unit's snapshot at at, unless the stored card is equal.
func (s cardStore) Save(ctx context.Context, c card.Card, at time.Time) (bool, error) {
	if c.Unit.IsZero() {
		return false, errors.New("card: no unit")
	}
	body, err := json.Marshal(c)
	if err != nil {
		return false, err
	}
	changed := false
	_, err = s.db.Write(ctx, func(tx *Tx) error {
		var old string
		switch err := tx.QueryRowContext(ctx, cardBodySQL, string(c.Unit.Provider), c.Unit.Key).Scan(&old); {
		case err == nil && bytes.Equal([]byte(old), body):
			return nil
		case err != nil && !errors.Is(err, sql.ErrNoRows):
			return err
		}
		if _, err := tx.ExecContext(ctx, upsertCardSQL, string(c.Unit.Provider), c.Unit.Key, string(body), formatTime(at)); err != nil {
			return fmt.Errorf("card of %s: %w", c.Unit, err)
		}
		changed = true
		tx.Touch(port.TopicCards, c.Unit)
		return nil
	})
	return changed, err
}

// Get is the stored snapshots of units; a unit with none is absent. Stale is
// never set here: the plugin's state is the caller's.
func (s cardStore) Get(ctx context.Context, units []unit.Unit) (map[unit.Unit]card.Snapshot, error) {
	out := make(map[unit.Unit]card.Snapshot, len(units))
	if len(units) == 0 {
		return out, nil
	}
	pairs, err := UnitPairs(units)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.R().QueryContext(ctx, cardsOfUnitsSQL, pairs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var u unit.Unit
		var body, at string
		if err := rows.Scan(&u.Provider, &u.Key, &body, &at); err != nil {
			return nil, err
		}
		var c card.Card
		if err := json.Unmarshal([]byte(body), &c); err != nil {
			return nil, fmt.Errorf("card of %s: %w", u, err)
		}
		out[u] = card.Snapshot{Card: c, At: parseTime(at)}
	}
	return out, rows.Err()
}
