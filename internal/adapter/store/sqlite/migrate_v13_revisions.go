package sqlite

// v13Revisions gives drafts a payload, a basis, a hold, the author of their content, a version that counts
// their writes and the cause of a failure; keeps every draft's content revisions; and gives draft gates the
// rev they approve and the sessions they stamp.
//
// A draft's rev counts its content from 1: a stored draft's first revision is its proposer's text, and an
// edited one's current text is a person's second. Its version counts every write, so it is its old rev plus
// one. An open or approved draft gate approves its draft's newest revision; 0, which never matches, is left
// on any other.
var v13Revisions = execAll(
	`ALTER TABLE drafts ADD COLUMN payload TEXT NOT NULL DEFAULT ''`,
	`ALTER TABLE drafts ADD COLUMN basis TEXT NOT NULL DEFAULT ''`,
	`ALTER TABLE drafts ADD COLUMN hold INTEGER NOT NULL DEFAULT 0`,
	`ALTER TABLE drafts ADD COLUMN by_kind TEXT NOT NULL DEFAULT ''`,
	`ALTER TABLE drafts ADD COLUMN by_session TEXT NOT NULL DEFAULT ''`,
	`ALTER TABLE drafts ADD COLUMN version INTEGER NOT NULL DEFAULT 1`,
	`ALTER TABLE drafts ADD COLUMN cause TEXT NOT NULL DEFAULT ''`,
	`ALTER TABLE gates ADD COLUMN approved_rev INTEGER NOT NULL DEFAULT 0`,
	`ALTER TABLE gates ADD COLUMN sessions TEXT NOT NULL DEFAULT '[]'`,
	`CREATE TABLE draft_revisions (
  draft_id INTEGER NOT NULL REFERENCES drafts (id) ON DELETE CASCADE,
  rev INTEGER NOT NULL,
  text TEXT NOT NULL, payload TEXT NOT NULL DEFAULT '', basis TEXT NOT NULL DEFAULT '',
  by_kind TEXT NOT NULL, by_session TEXT NOT NULL DEFAULT '',
  at TEXT NOT NULL,
  PRIMARY KEY (draft_id, rev)
) WITHOUT ROWID`,
	`INSERT INTO draft_revisions (draft_id, rev, text, by_kind, by_session, at)
	SELECT id, 1, CASE WHEN edited = 1 THEN original ELSE text END,
		CASE WHEN origin_session != '' OR origin_kind IN ('turn', 'session') THEN 'session' WHEN origin_kind = 'auto' THEN 'rule' ELSE 'person' END,
		origin_session, created_at
	FROM drafts`,
	`INSERT INTO draft_revisions (draft_id, rev, text, by_kind, at)
	SELECT id, 2, text, 'person', updated_at FROM drafts WHERE edited = 1 AND text != original AND rev > 0`,
	`UPDATE drafts SET version = rev + 1`,
	`UPDATE drafts SET (rev, by_kind, by_session) = (SELECT r.rev, r.by_kind, r.by_session FROM draft_revisions r
		WHERE r.draft_id = drafts.id ORDER BY r.rev DESC LIMIT 1)`,
	draftGatesApproveTheirDraftsRevSQL,
	draftGatesStampTheirSessionSQL,
	`UPDATE drafts SET cause = 'unconfirmed' WHERE error = 'not confirmed after a restart' OR error LIKE 'may have been posted%'`,
)
