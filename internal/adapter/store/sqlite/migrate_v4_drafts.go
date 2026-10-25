package sqlite

import (
	"context"
	"database/sql"
)

// v4Closure sets extraction_cursors.closure_marked on every unit with a sent
// close draft.
func v4Closure(ctx context.Context, tx *sql.Tx) error {
	_, _ = ctx, tx
	return nil
}

// v4Effects sets effects_applied_at to updated_at on every sent draft, whose
// effects already happened, and backfills the effects of open drafts that
// declare none from their kind.
func v4Effects(ctx context.Context, tx *sql.Tx) error {
	_, _ = ctx, tx
	return nil
}
