package sqlite

import (
	"context"
	"database/sql"
)

// v7Anchors gives each event the point of its unit it happened at, such as a review comment, which a
// plugin's events carry: its anchor's JSON, empty for none.
func v7Anchors(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `ALTER TABLE events ADD COLUMN anchor TEXT NOT NULL DEFAULT ''`)
	return err
}
