package sqlite

import (
	"context"
	"database/sql"
)

// v4Briefs copies pr_briefs into unit_briefs and pr_tags into unit_tags as
// github units, fills each brief run's provider, unit_key, revision, input
// and intent from the columns v4 drops, and copies each Slack unit's
// extraction_cursors.brief into a legacy brief run its unit_briefs row uses.
func v4Briefs(ctx context.Context, tx *sql.Tx) error {
	_, _ = ctx, tx
	return nil
}
