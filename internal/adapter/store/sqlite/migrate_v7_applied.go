package sqlite

import (
	"context"
	"database/sql"
)

// v7Applied marks every gate resolved so far as applied, its consequence having run before version 7 or by an
// earlier step, so the gates' first tick replays none of them; and gives each item proposal the time a decision
// of it was applied.
func v7Applied(ctx context.Context, tx *sql.Tx) error {
	for _, stmt := range []string{
		`ALTER TABLE gates ADD COLUMN applied_at TEXT NOT NULL DEFAULT ''`,
		`UPDATE gates SET applied_at = closed_at WHERE status != 'open'`,
		`ALTER TABLE gates ADD COLUMN begun_at TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE item_proposals ADD COLUMN decided_at TEXT NOT NULL DEFAULT ''`,
	} {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return err
		}
	}
	return nil
}
