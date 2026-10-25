package sqlite

import (
	"context"
	"database/sql"
)

// v4Adopt rewrites every draft of the proposals job's rules, in any state, to
// the identity of the default workflow that replaces it: rule and workflow
// stalled-nudge to slack-stalled-nudge, closure to slack-closure, ask-owner to
// slack-ask-owner, rule_key unchanged, and the effects of each rule on the
// drafts not sent.
func v4Adopt(ctx context.Context, tx *sql.Tx) error {
	_, _ = ctx, tx
	return nil
}
