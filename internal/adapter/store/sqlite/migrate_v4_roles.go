package sqlite

import (
	"context"
	"database/sql"
)

// v4Roles backfills turns.role from the bundled intents' names: review to
// pr.review, address to pr.address, nudge to pr.nudge, probe to probe and
// act to headless. A user intent's turns keep no role: before v4 no user
// intent could hold one.
func v4Roles(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `UPDATE turns SET role = CASE intent
		WHEN 'review' THEN 'pr.review'
		WHEN 'address' THEN 'pr.address'
		WHEN 'nudge' THEN 'pr.nudge'
		WHEN 'probe' THEN 'probe'
		WHEN 'act' THEN 'headless'
		ELSE '' END
		WHERE role = ''`)
	return err
}
