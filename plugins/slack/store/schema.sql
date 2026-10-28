CREATE TABLE segment_map (
  provider TEXT NOT NULL, conversation TEXT NOT NULL, segment_id TEXT NOT NULL,
  team TEXT NOT NULL, from_ts TEXT NOT NULL, to_ts TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL, updated_at TEXT NOT NULL, closed INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (provider, conversation, segment_id)
) WITHOUT ROWID;

CREATE TABLE extraction_cursors (
  provider TEXT NOT NULL, unit_key TEXT NOT NULL,
  last_ts TEXT NOT NULL DEFAULT '', last_seq INTEGER NOT NULL DEFAULT 0,
  verdict TEXT NOT NULL DEFAULT '', verdict_why TEXT NOT NULL DEFAULT '',
  viewer_role TEXT NOT NULL DEFAULT '', topic TEXT NOT NULL DEFAULT '',
  started_by_viewer INTEGER NOT NULL DEFAULT 0, closure_posted INTEGER NOT NULL DEFAULT 0,
  watched INTEGER NOT NULL DEFAULT 0, at TEXT NOT NULL,
  -- a sent draft's unit.closed; the extraction never writes it, readers OR it with closure_posted
  closure_marked INTEGER NOT NULL DEFAULT 0,
  -- the JSON of the brief the extraction last wrote of the unit
  brief TEXT NOT NULL DEFAULT '',
  PRIMARY KEY (provider, unit_key)
) WITHOUT ROWID;

CREATE TABLE extraction_hints (
  provider TEXT NOT NULL, unit_key TEXT NOT NULL, item_id TEXT NOT NULL,
  mode TEXT NOT NULL DEFAULT '', context TEXT NOT NULL DEFAULT '', why TEXT NOT NULL DEFAULT '',
  PRIMARY KEY (provider, unit_key, item_id)
) WITHOUT ROWID;

CREATE TABLE extraction_days (
  day TEXT PRIMARY KEY, calls INTEGER NOT NULL DEFAULT 0, cost_usd REAL NOT NULL DEFAULT 0
) WITHOUT ROWID;

CREATE TABLE extraction_runs (
  id INTEGER PRIMARY KEY,
  provider TEXT NOT NULL, unit_key TEXT NOT NULL, at TEXT NOT NULL,
  model TEXT NOT NULL DEFAULT '', cost_usd REAL, outcome TEXT NOT NULL, error TEXT NOT NULL DEFAULT ''
);

CREATE TABLE slack_conversations (
  id TEXT PRIMARY KEY,
  team TEXT NOT NULL DEFAULT '', name TEXT NOT NULL DEFAULT '', kind TEXT NOT NULL DEFAULT '',
  is_member INTEGER NOT NULL DEFAULT 0, archived INTEGER NOT NULL DEFAULT 0, dm_user TEXT NOT NULL DEFAULT '',
  hwm TEXT NOT NULL DEFAULT '', admitted INTEGER NOT NULL DEFAULT 0, updated_at TEXT NOT NULL DEFAULT ''
) WITHOUT ROWID;

CREATE TABLE slack_objects (
  object TEXT PRIMARY KEY,
  conversation TEXT NOT NULL, ts TEXT NOT NULL DEFAULT '', kind TEXT NOT NULL CHECK (kind IN ('thread', 'dm')),
  version TEXT NOT NULL DEFAULT '', latest_reply TEXT NOT NULL DEFAULT '', reply_count INTEGER NOT NULL DEFAULT 0,
  watched INTEGER NOT NULL DEFAULT 0, reasons TEXT NOT NULL DEFAULT '[]',
  last_activity_at TEXT NOT NULL DEFAULT '', next_check_at TEXT NOT NULL DEFAULT '',
  seen_at TEXT NOT NULL DEFAULT '', admitted_at TEXT NOT NULL DEFAULT ''
) WITHOUT ROWID;
CREATE INDEX slack_objects_due ON slack_objects (next_check_at) WHERE watched = 1;

CREATE TABLE slack_messages (
  conversation TEXT NOT NULL, ts TEXT NOT NULL,
  thread_ts TEXT NOT NULL DEFAULT '', parent_user TEXT NOT NULL DEFAULT '',
  user TEXT NOT NULL DEFAULT '', bot_id TEXT NOT NULL DEFAULT '', app_id TEXT NOT NULL DEFAULT '', username TEXT NOT NULL DEFAULT '',
  subtype TEXT NOT NULL DEFAULT '', text TEXT NOT NULL DEFAULT '', edited_ts TEXT NOT NULL DEFAULT '',
  deleted INTEGER NOT NULL DEFAULT 0,
  files TEXT NOT NULL DEFAULT '[]', links TEXT NOT NULL DEFAULT '[]', mentions TEXT NOT NULL DEFAULT '[]',
  permalink TEXT NOT NULL DEFAULT '', via TEXT NOT NULL DEFAULT '', received_at TEXT NOT NULL DEFAULT '',
  PRIMARY KEY (conversation, ts)
) WITHOUT ROWID;
CREATE INDEX slack_messages_anchor ON slack_messages (conversation, (CASE WHEN thread_ts = '' THEN ts ELSE thread_ts END), ts);

CREATE TABLE slack_reactions (
  conversation TEXT NOT NULL, ts TEXT NOT NULL, name TEXT NOT NULL, user TEXT NOT NULL,
  PRIMARY KEY (conversation, ts, name, user)
) WITHOUT ROWID;

CREATE TABLE slack_users (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL DEFAULT '', display_name TEXT NOT NULL DEFAULT '', email TEXT NOT NULL DEFAULT '',
  is_bot INTEGER NOT NULL DEFAULT 0, deleted INTEGER NOT NULL DEFAULT 0, fetched_at TEXT NOT NULL DEFAULT ''
) WITHOUT ROWID;
CREATE INDEX slack_users_email ON slack_users (email COLLATE NOCASE);

CREATE TABLE slack_changes (
  seq INTEGER PRIMARY KEY AUTOINCREMENT,
  conversation TEXT NOT NULL, ts TEXT NOT NULL, thread_ts TEXT NOT NULL DEFAULT '',
  kind TEXT NOT NULL CHECK (kind IN ('new', 'edited', 'deleted', 'reaction')),
  edited_ts TEXT NOT NULL DEFAULT '', at TEXT NOT NULL,
  UNIQUE (conversation, ts, kind, edited_ts)
);

CREATE TABLE slack_cursors (
  name TEXT PRIMARY KEY, value TEXT NOT NULL, at TEXT NOT NULL
) WITHOUT ROWID;
