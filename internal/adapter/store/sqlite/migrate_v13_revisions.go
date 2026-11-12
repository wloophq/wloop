package sqlite

// v13Revisions gives drafts a payload, a basis, a hold and the author of their content, keeps every draft's
// content revisions, and gives draft gates the rev they approve and the sessions they stamp.
//
// A draft's rev becomes its content's revision, and its old write counter its version: an edited draft is at
// rev 2, any other at rev 1, so a gate whose ApprovedRev is 0 never matches. A stored draft's first revision
// is its proposer's text at rev 1, and an edited one's current text is a person's at rev 2. An open or
// approved draft gate approves its draft's rev.
var v13Revisions = execAll(
	`ALTER TABLE drafts ADD COLUMN payload TEXT NOT NULL DEFAULT ''`,
	`ALTER TABLE drafts ADD COLUMN basis TEXT NOT NULL DEFAULT ''`,
	`ALTER TABLE drafts ADD COLUMN hold INTEGER NOT NULL DEFAULT 0`,
	`ALTER TABLE drafts ADD COLUMN by_kind TEXT NOT NULL DEFAULT ''`,
	`ALTER TABLE drafts ADD COLUMN by_session TEXT NOT NULL DEFAULT ''`,
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
	`ALTER TABLE drafts ADD COLUMN version INTEGER NOT NULL DEFAULT 1`,
	`UPDATE drafts SET version = rev + 1, rev = CASE WHEN edited = 1 AND text != original THEN 2 ELSE 1 END`,
	`INSERT INTO draft_revisions (draft_id, rev, text, by_kind, by_session, at)
	SELECT id, 1, CASE WHEN edited = 1 THEN original ELSE text END,
		CASE WHEN origin_session != '' OR origin_kind IN ('turn', 'session') THEN 'session' WHEN origin_kind = 'auto' THEN 'rule' ELSE 'person' END,
		origin_session, created_at
	FROM drafts`,
	`INSERT INTO draft_revisions (draft_id, rev, text, by_kind, at)
	SELECT id, rev, text, 'person', updated_at FROM drafts WHERE rev = 2`,
	`UPDATE drafts SET (by_kind, by_session) = (SELECT r.by_kind, r.by_session FROM draft_revisions r
		WHERE r.draft_id = drafts.id ORDER BY r.rev DESC LIMIT 1)`,
	draftGatesApproveTheirDraftsRevSQL,
	draftGatesStampTheirSessionSQL,
)
