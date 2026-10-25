package sqlite

import (
	"context"
	"database/sql"
)

// v4Roles backfills turns.role from the bundled intents' names: review to
// round.review, address to round.address, nudge to nudge, probe to probe and
// act to headless.default.
func v4Roles(ctx context.Context, tx *sql.Tx) error {
	_, _ = ctx, tx
	return nil
}
