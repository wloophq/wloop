CREATE TABLE pull_requests (
  owner TEXT NOT NULL, repo TEXT NOT NULL, number INTEGER NOT NULL,
  title TEXT NOT NULL DEFAULT '', author TEXT NOT NULL DEFAULT '',
  state TEXT NOT NULL DEFAULT 'OPEN', is_draft INTEGER NOT NULL DEFAULT 0,
  head_oid TEXT NOT NULL DEFAULT '', head_branch TEXT NOT NULL DEFAULT '', base_branch TEXT NOT NULL DEFAULT '',
  is_fork INTEGER NOT NULL DEFAULT 0, checks_state TEXT NOT NULL DEFAULT '', review_decision TEXT NOT NULL DEFAULT '',
  review_requests TEXT NOT NULL DEFAULT '',
  url TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL DEFAULT '', updated_at TEXT NOT NULL DEFAULT '', merged_at TEXT NOT NULL DEFAULT '', closed_at TEXT NOT NULL DEFAULT '',
  body_edited_at TEXT NOT NULL DEFAULT '', changed_files INTEGER NOT NULL DEFAULT 0,
  reasons TEXT NOT NULL DEFAULT '',
  ws_id TEXT NOT NULL DEFAULT '', ws_name TEXT NOT NULL DEFAULT '', ws_status TEXT NOT NULL DEFAULT '', ws_review INTEGER NOT NULL DEFAULT 0, ws_agent TEXT NOT NULL DEFAULT '',
  ws_comment TEXT NOT NULL DEFAULT '', ws_main INTEGER NOT NULL DEFAULT 0, ws_manager TEXT NOT NULL DEFAULT '',
  failing_checks TEXT NOT NULL DEFAULT '',
  mergeable TEXT NOT NULL DEFAULT '',
  mergeable_at TEXT NOT NULL DEFAULT '',
  PRIMARY KEY (owner, repo, number)
);
CREATE INDEX pr_state ON pull_requests(state, updated_at);

CREATE TABLE pr_seen (
  owner TEXT NOT NULL, repo TEXT NOT NULL, number INTEGER NOT NULL,
  at TEXT NOT NULL,
  PRIMARY KEY (owner, repo, number)
) WITHOUT ROWID;

-- A pull request's full read beyond its row, kept apart so the rows stay
-- small; body is the detail as JSON.
CREATE TABLE pr_details (
  owner TEXT NOT NULL, repo TEXT NOT NULL, number INTEGER NOT NULL,
  version INTEGER NOT NULL,
  read_at TEXT NOT NULL,
  body TEXT NOT NULL,
  PRIMARY KEY (owner, repo, number)
) WITHOUT ROWID;

CREATE TABLE fetches (
  workspace_id TEXT PRIMARY KEY,
  at TEXT NOT NULL
) WITHOUT ROWID;

CREATE TABLE meta (key TEXT PRIMARY KEY, value TEXT NOT NULL);

-- What the user set on a pull request, keyed by its unit key.
CREATE TABLE overrides (
  unit_key TEXT PRIMARY KEY,
  taken_over INTEGER NOT NULL DEFAULT 0,
  tracked INTEGER NOT NULL DEFAULT 0
) WITHOUT ROWID;
