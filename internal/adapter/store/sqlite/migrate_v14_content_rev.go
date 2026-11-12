package sqlite

// v14ContentRev makes a draft's rev count its content and gives it a version that counts its writes. It drops
// migration 13's trigger, which carried a gate's approval across writes in the store; renumbers each draft's
// revisions 1, 2, ... in order and sets its rev to its newest; and keeps a draft gate's approval only when the
// gate was at its draft's rev, which no write had passed, mapped to that content rev, any other to 0.
var v14ContentRev = execAll(
	`DROP TRIGGER IF EXISTS drafts_rev_keeps_approval`,
	`ALTER TABLE drafts ADD COLUMN version INTEGER NOT NULL DEFAULT 1`,
	`UPDATE drafts SET version = max(rev, 1)`,
	`CREATE TEMP TABLE v14_revisions AS SELECT draft_id, row_number() OVER (PARTITION BY draft_id ORDER BY rev) AS rev,
		text, payload, basis, by_kind, by_session, at FROM draft_revisions`,
	draftGatesAtContentRevsSQL,
	`UPDATE drafts SET rev = coalesce((SELECT max(r.rev) FROM v14_revisions r WHERE r.draft_id = drafts.id), 1)`,
	`DELETE FROM draft_revisions`,
	`INSERT INTO draft_revisions (draft_id, rev, text, payload, basis, by_kind, by_session, at)
	SELECT draft_id, rev, text, payload, basis, by_kind, by_session, at FROM v14_revisions`,
	`DROP TABLE v14_revisions`,
)
