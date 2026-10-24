package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/wloophq/wloop/internal/domain/unit"
)

// Classifications caches classify outputs by unit, workflow and the unit's
// fingerprint, and counts the calls of each local day for the daily cap.
// Its writes note nothing.
type Classifications struct{ db *DB }

func (db *DB) Classifications() *Classifications { return &Classifications{db} }

// Output is the stored output of workflow for u at fingerprint.
func (c *Classifications) Output(ctx context.Context, u unit.Unit, workflow, fingerprint string) (json.RawMessage, bool, error) {
	var out string
	err := c.db.R().QueryRowContext(ctx, `SELECT output FROM classifications
		WHERE provider = ? AND unit_key = ? AND fingerprint = ? AND workflow = ?`,
		string(u.Provider), u.Key, fingerprint, workflow).Scan(&out)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return json.RawMessage(out), true, nil
}

// Save stores output, a JSON value, replacing the one stored for the same
// unit, workflow and fingerprint.
func (c *Classifications) Save(ctx context.Context, u unit.Unit, workflow, fingerprint string, output json.RawMessage, at time.Time) error {
	if !json.Valid(output) {
		return fmt.Errorf("the %s classification of %s is not JSON", workflow, u)
	}
	_, err := c.db.Write(ctx, func(tx *Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT OR REPLACE INTO classifications (provider, unit_key, fingerprint, workflow, output, at)
			VALUES (?, ?, ?, ?, ?, ?)`, string(u.Provider), u.Key, fingerprint, workflow, string(output), eventTime(at))
		return err
	})
	return err
}

// Spend counts one classify call on the day of at in at's location when
// fewer than limit are counted there, and says whether it did.
func (c *Classifications) Spend(ctx context.Context, at time.Time, limit int) (bool, error) {
	var spent bool
	_, err := c.db.Write(ctx, func(tx *Tx) error {
		res, err := tx.ExecContext(ctx, `INSERT INTO classify_days (day, total) SELECT ?, 1 WHERE ? > 0
			ON CONFLICT (day) DO UPDATE SET total = total + 1 WHERE total < ?`, localDay(at), limit, limit)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		spent = n == 1
		return err
	})
	return spent, err
}

// Spent is how many calls Spend counted on the day of at in at's location.
func (c *Classifications) Spent(ctx context.Context, at time.Time) (int, error) {
	var n int
	err := c.db.R().QueryRowContext(ctx, `SELECT coalesce((SELECT total FROM classify_days WHERE day = ?), 0)`, localDay(at)).Scan(&n)
	return n, err
}

func localDay(at time.Time) string { return at.Format(time.DateOnly) }
