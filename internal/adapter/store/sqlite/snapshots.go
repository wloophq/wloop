package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/domain/snapshot"
	"github.com/wloophq/wloop/internal/domain/unit"
)

type snapshotStore struct{ db *DB }

var _ snapshot.Store = snapshotStore{}

// Snapshots is the store of the units' reported snapshots.
func (db *DB) Snapshots() snapshot.Store { return snapshotStore{db} }

const (
	upsertSnapshotSQL = `INSERT INTO unit_snapshots (provider, unit_key, snapshot, rev, at) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (provider, unit_key) DO UPDATE SET snapshot = excluded.snapshot, rev = excluded.rev, at = excluded.at`
	deleteSnapshotSQL = `DELETE FROM unit_snapshots WHERE provider = ? AND unit_key = ?`
	snapshotsSQL      = `SELECT provider, unit_key, snapshot, rev, at FROM unit_snapshots
		WHERE (provider, unit_key) IN (SELECT value->>0, value->>1 FROM json_each(?))`
	snapshotRevsSQL = `SELECT provider, unit_key, rev FROM unit_snapshots
		WHERE (provider, unit_key) IN (SELECT value->>0, value->>1 FROM json_each(?))`
	snapshotUnitsSQL = `SELECT unit_key FROM unit_snapshots WHERE provider = ? ORDER BY unit_key`
)

func (s snapshotStore) Put(ctx context.Context, puts []snapshot.Stored, gone []unit.Unit) error {
	if len(puts) == 0 && len(gone) == 0 {
		return nil
	}
	_, err := s.db.Write(ctx, func(tx *Tx) error {
		for _, p := range puts {
			if p.Unit.IsZero() {
				return errors.New("snapshot: no unit")
			}
			if _, err := tx.ExecContext(ctx, upsertSnapshotSQL, string(p.Unit.Provider), p.Unit.Key, string(p.JSON),
				p.Rev, formatTime(p.At)); err != nil {
				return fmt.Errorf("snapshot of %s: %w", p.Unit, err)
			}
			tx.Touch(port.TopicPRs, p.Unit)
		}
		for _, u := range gone {
			if _, err := tx.ExecContext(ctx, deleteSnapshotSQL, string(u.Provider), u.Key); err != nil {
				return fmt.Errorf("snapshot of %s: %w", u, err)
			}
			tx.Touch(port.TopicPRs, u)
		}
		return nil
	})
	return err
}

func (s snapshotStore) Get(ctx context.Context, units []unit.Unit) (map[unit.Unit]snapshot.Stored, error) {
	out := make(map[unit.Unit]snapshot.Stored, len(units))
	if len(units) == 0 {
		return out, nil
	}
	pairs, err := unitPairs(units)
	if err != nil {
		return nil, err
	}
	err = s.db.Read(ctx, func(tx *sql.Tx) error {
		return eachRow(ctx, tx, snapshotsSQL, []any{pairs}, func(r *sql.Rows) error {
			var provider, key, body, at string
			var rev uint64
			if err := r.Scan(&provider, &key, &body, &rev, &at); err != nil {
				return err
			}
			u := unit.Unit{Provider: unit.ProviderID(provider), Key: key}
			out[u] = snapshot.Stored{Unit: u, Rev: rev, At: parseTime(at), JSON: []byte(body)}
			return nil
		})
	})
	return out, err
}

func (s snapshotStore) Revs(ctx context.Context, units []unit.Unit) (map[unit.Unit]uint64, error) {
	out := make(map[unit.Unit]uint64, len(units))
	if len(units) == 0 {
		return out, nil
	}
	pairs, err := unitPairs(units)
	if err != nil {
		return nil, err
	}
	err = s.db.Read(ctx, func(tx *sql.Tx) error {
		return eachRow(ctx, tx, snapshotRevsSQL, []any{pairs}, func(r *sql.Rows) error {
			var provider, key string
			var rev uint64
			if err := r.Scan(&provider, &key, &rev); err != nil {
				return err
			}
			out[unit.Unit{Provider: unit.ProviderID(provider), Key: key}] = rev
			return nil
		})
	})
	return out, err
}

// unitPairs are units as the JSON list of [provider, key] a query reads
// with json_each.
func unitPairs(units []unit.Unit) (string, error) {
	pairs := make([][2]string, len(units))
	for i, u := range units {
		pairs[i] = [2]string{string(u.Provider), u.Key}
	}
	raw, err := json.Marshal(pairs)
	return string(raw), err
}

func (s snapshotStore) Units(ctx context.Context, p unit.ProviderID) ([]unit.Unit, error) {
	var out []unit.Unit
	err := s.db.Read(ctx, func(tx *sql.Tx) error {
		return eachRow(ctx, tx, snapshotUnitsSQL, []any{string(p)}, func(r *sql.Rows) error {
			var key string
			if err := r.Scan(&key); err != nil {
				return err
			}
			out = append(out, unit.Unit{Provider: p, Key: key})
			return nil
		})
	})
	return out, err
}
