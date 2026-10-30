package store

import (
	"context"
	"fmt"
	"time"
)

// LastFetch is the last fetch of workspace id, zero when it was never
// fetched.
func (db *DB) LastFetch(ctx context.Context, id string) (time.Time, error) {
	var at string
	err := db.db.QueryRowContext(ctx, `SELECT at FROM fetches WHERE workspace_id = ?`, id).Scan(&at)
	if noRows(err) {
		return time.Time{}, nil
	}
	if err != nil {
		return time.Time{}, err
	}
	last, err := parseTime(at)
	if err != nil {
		return time.Time{}, fmt.Errorf("the last fetch of %s: %w", id, err)
	}
	return last, nil
}

// MarkFetch records a fetch of workspace id at at, which changes no
// reported fact.
func (db *DB) MarkFetch(ctx context.Context, id string, at time.Time) error {
	if at.IsZero() {
		return fmt.Errorf("a fetch of %s without a time", id)
	}
	_, err := db.Write(ctx, func(tx *Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT OR REPLACE INTO fetches (workspace_id, at) VALUES (?, ?)`, id, formatTime(at))
		return err
	})
	return err
}
