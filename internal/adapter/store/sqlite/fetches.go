package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Fetches is when each workspace was last fetched. Its writes note nothing.
type Fetches struct{ db *DB }

func (db *DB) Fetches() *Fetches { return &Fetches{db} }

// Last is the last fetch of workspace id, zero when it was never fetched.
func (f *Fetches) Last(ctx context.Context, id string) (time.Time, error) {
	var at string
	err := f.db.R().QueryRowContext(ctx, `SELECT at FROM fetches WHERE workspace_id = ?`, id).Scan(&at)
	if errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, nil
	}
	if err != nil {
		return time.Time{}, err
	}
	last, err := parseStoredTime(at)
	if err != nil {
		return time.Time{}, fmt.Errorf("the last fetch of %s: %w", id, err)
	}
	return last, nil
}

func (f *Fetches) Mark(ctx context.Context, id string, at time.Time) error {
	if at.IsZero() {
		return fmt.Errorf("a fetch of %s without a time", id)
	}
	_, err := f.db.Write(ctx, func(tx *Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT OR REPLACE INTO fetches (workspace_id, at) VALUES (?, ?)`, id, eventTime(at))
		return err
	})
	return err
}
