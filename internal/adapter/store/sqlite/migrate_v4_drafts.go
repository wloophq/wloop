package sqlite

import (
	"context"
	"database/sql"
)

// v4Closure sets extraction_cursors.closure_marked on every unit with a sent
// close draft: before v4 the closure was read from its anchor, which new
// drafts no longer carry.
func v4Closure(ctx context.Context, tx *sql.Tx) error {
	return execAll(`UPDATE extraction_cursors SET closure_marked = 1 WHERE EXISTS (
  SELECT 1 FROM drafts d WHERE d.provider = extraction_cursors.provider AND d.unit_key = extraction_cursors.unit_key
    AND d.state = 'sent' AND (d.kind = 'close' OR EXISTS (SELECT 1 FROM json_each(d.effects) WHERE value = 'unit.closed')))`)(ctx, tx)
}

// v4Effects sets effects_applied_at to updated_at on every sent draft, whose
// effects already happened, and backfills the effects of open drafts that
// declare none from their kind; an open GitHub nudge also records its
// nudged event, which its publisher did by kind before.
func v4Effects(ctx context.Context, tx *sql.Tx) error {
	return execAll(
		`UPDATE drafts SET effects_applied_at = updated_at WHERE state = 'sent'`,
		`UPDATE drafts SET effects = '["entry.nudged"]' WHERE kind = 'nudge' AND effects = '[]' AND state IN ('proposed', 'approved', 'sending', 'failed')`,
		`UPDATE drafts SET effects = '["unit.closed"]' WHERE kind = 'close' AND effects = '[]' AND state IN ('proposed', 'approved', 'sending', 'failed')`,
		`UPDATE drafts SET effects = json_insert(effects, '$[#]', 'pr.nudged') WHERE provider = 'github' AND kind = 'nudge'
  AND state IN ('proposed', 'approved', 'sending', 'failed') AND NOT EXISTS (SELECT 1 FROM json_each(effects) WHERE value = 'pr.nudged')`,
	)(ctx, tx)
}

// v4Approval writes the draft approval modes (always, window, none, observe
// to wait, delay, send, dry_run), the reason and outcome of a draft stored
// without sending (observe to dry_run, observed to dry_run), the outcome of
// a released draft (auto to released) and the would policies (send to
// release, hold to wait).
func v4Approval(ctx context.Context, tx *sql.Tx) error {
	return execAll(
		`UPDATE drafts SET approval_mode = CASE approval_mode WHEN 'always' THEN 'wait' WHEN 'window' THEN 'delay'
  WHEN 'none' THEN 'send' WHEN 'observe' THEN 'dry_run' ELSE approval_mode END`,
		`UPDATE drafts SET reason = 'dry_run' WHERE state = 'discarded' AND reason = 'observe'`,
		`UPDATE drafts SET would_outcome = CASE would_outcome WHEN 'observed' THEN 'dry_run' WHEN 'auto' THEN 'released' ELSE would_outcome END`,
		`UPDATE drafts SET would_policy = CASE would_policy WHEN 'send' THEN 'release' WHEN 'hold' THEN 'wait' ELSE would_policy END`,
	)(ctx, tx)
}
