package sqlite

import (
	"context"
	"database/sql"
)

// v6FirstSkipped is the mark version 7 keeps a skipped first round in, which version 6 kept in
// round_state.first_round_skipped: the GitHub planner's mark, carried here as history.
const v6FirstSkipped = "first.skipped"

// v7Rounds moves the first round skip of round_state into the unit's marks and drops its column.
func v7Rounds(ctx context.Context, tx *sql.Tx) error {
	if _, err := tx.ExecContext(ctx, `INSERT INTO unit_marks (provider, unit_key, mark, value)
		SELECT provider, unit_key, ?, '1' FROM round_state WHERE first_round_skipped != 0
		ON CONFLICT DO NOTHING`, v6FirstSkipped); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `ALTER TABLE round_state DROP COLUMN first_round_skipped`)
	return err
}
