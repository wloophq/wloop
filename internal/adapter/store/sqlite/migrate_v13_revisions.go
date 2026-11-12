package sqlite

// v13Revisions gives drafts a payload, a basis, a hold and the author of their content, keeps every draft's
// content revisions, and gives draft gates the rev they approve and the sessions they stamp.
//
// A draft's rev now starts at 1, so every stored rev moves up by one: a gate whose ApprovedRev is 0 never
// matches. A stored draft's first revision is its proposer's text at rev 1, and an edited one's current text
// is a person's at its rev. An open or approved draft gate approves its draft's rev.
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
	`UPDATE drafts SET rev = rev + 1`,
	`INSERT INTO draft_revisions (draft_id, rev, text, by_kind, by_session, at)
	SELECT id, 1, CASE WHEN edited = 1 THEN original ELSE text END,
		CASE WHEN origin_session != '' OR origin_kind IN ('turn', 'session') THEN 'session' WHEN origin_kind = 'auto' THEN 'rule' ELSE 'person' END,
		origin_session, created_at
	FROM drafts`,
	`INSERT INTO draft_revisions (draft_id, rev, text, by_kind, at)
	SELECT id, rev, text, 'person', updated_at FROM drafts WHERE edited = 1 AND text != original AND rev > 1`,
	`UPDATE drafts SET (by_kind, by_session) = (SELECT r.by_kind, r.by_session FROM draft_revisions r
		WHERE r.draft_id = drafts.id ORDER BY r.rev DESC LIMIT 1)`,
	`UPDATE gates SET approved_rev = coalesce((SELECT d.rev FROM drafts d WHERE d.id = CAST(gates.subject AS INTEGER)), 0)
	WHERE kind = 'draft' AND status IN ('open', 'approved')`,
	`UPDATE gates SET sessions = json_array(session) WHERE kind = 'draft' AND session != ''`,
	`CREATE TRIGGER drafts_rev_keeps_approval AFTER UPDATE OF rev ON drafts
WHEN NEW.rev != OLD.rev AND NEW.text = OLD.text AND NEW.payload = OLD.payload AND NEW.basis = OLD.basis
BEGIN
  UPDATE gates SET approved_rev = NEW.rev
  WHERE provider = NEW.provider AND unit_key IN (OLD.unit_key, NEW.unit_key) AND kind = 'draft'
  AND subject = CAST(NEW.id AS TEXT) AND status IN ('open', 'approved') AND approved_rev = OLD.rev;
END`,
)
