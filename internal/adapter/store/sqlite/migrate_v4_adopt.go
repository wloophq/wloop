package sqlite

import (
	"context"
	"database/sql"
)

// v4Adopt is AdoptJobDrafts as a v4 step.
func v4Adopt(ctx context.Context, tx *sql.Tx) error { return AdoptJobDrafts(ctx, tx) }

// AdoptJobDrafts rewrites every draft the proposals job proposed under its
// rules, in any state, to the identity of the default workflow that
// replaced the rule: rule and workflow stalled-nudge to slack-stalled-nudge,
// closure to slack-closure, ask-owner to slack-ask-owner, rule_key
// unchanged, so the workflow's hold and withdrawal see them; a draft not
// sent gets the workflow's effects.
func AdoptJobDrafts(ctx context.Context, tx *sql.Tx) error {
	return execAll(
		`UPDATE drafts SET effects = CASE rule WHEN 'stalled-nudge' THEN '["entry.nudged"]' WHEN 'closure' THEN '["unit.closed"]' ELSE '[]' END
  WHERE origin_kind = 'auto' AND rule IN ('stalled-nudge', 'closure', 'ask-owner') AND state != 'sent'`,
		`UPDATE drafts SET rule = 'slack-' || rule, workflow = 'slack-' || rule
  WHERE origin_kind = 'auto' AND rule IN ('stalled-nudge', 'closure', 'ask-owner')`,
	)(ctx, tx)
}
