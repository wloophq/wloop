package sqlite

import (
	"context"
	"database/sql"
)

// v4Roles backfills turns.role from the bundled intents' names: review to
// round.review, address to round.address, nudge to nudge, probe to probe and
// act to headless.default. A user intent's turns keep no role: before v4 no
// user intent could hold one.
func v4Roles(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `UPDATE turns SET role = CASE intent
		WHEN 'review' THEN 'round.review'
		WHEN 'address' THEN 'round.address'
		WHEN 'nudge' THEN 'nudge'
		WHEN 'probe' THEN 'probe'
		WHEN 'act' THEN 'headless.default'
		ELSE '' END
		WHERE role = ''`)
	return err
}
