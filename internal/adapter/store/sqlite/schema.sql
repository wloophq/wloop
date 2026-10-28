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

-- An event of no unit (a pause) has an empty provider and unit_key.
CREATE TABLE events (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  provider TEXT NOT NULL, unit_key TEXT NOT NULL,
  kind TEXT NOT NULL, key TEXT NOT NULL DEFAULT '', actor TEXT NOT NULL DEFAULT '',
  at TEXT NOT NULL, data TEXT NOT NULL DEFAULT ''
);
CREATE UNIQUE INDEX events_keyed ON events(provider, unit_key, kind, key) WHERE key != '';
CREATE UNIQUE INDEX events_unkeyed ON events(provider, unit_key, kind, at) WHERE key = '';
CREATE INDEX events_unit ON events(provider, unit_key, at);
CREATE INDEX events_unit_kind ON events(provider, unit_key, kind, at);
CREATE INDEX events_recent ON events(at DESC, kind, key);
CREATE INDEX events_kind ON events(kind, at);

CREATE TABLE overrides (
  provider TEXT NOT NULL, unit_key TEXT NOT NULL,
  muted_until TEXT NOT NULL DEFAULT '', mute_until_state INTEGER NOT NULL DEFAULT 0,
  hold INTEGER NOT NULL DEFAULT 0, taken_over INTEGER NOT NULL DEFAULT 0,
  muted_kind TEXT NOT NULL DEFAULT '', muted_waiting_on TEXT NOT NULL DEFAULT '',
  next_guidance TEXT NOT NULL DEFAULT '',
  tracked INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (provider, unit_key)
);

CREATE TABLE meta (key TEXT PRIMARY KEY, value TEXT NOT NULL);

CREATE TABLE turn_instructions (
  sha TEXT PRIMARY KEY, kind TEXT NOT NULL, repo TEXT NOT NULL,
  created_at TEXT NOT NULL,
  bundled_sha TEXT NOT NULL DEFAULT '',
  sources TEXT NOT NULL DEFAULT '',
  parts TEXT NOT NULL DEFAULT ''
);
CREATE INDEX turn_instructions_kind ON turn_instructions(kind, repo, created_at);

CREATE TABLE instruction_parts (
  sha TEXT PRIMARY KEY, body BLOB NOT NULL
) WITHOUT ROWID;

CREATE TABLE round_state (
  provider TEXT NOT NULL, unit_key TEXT NOT NULL,
  sent_at TEXT NOT NULL DEFAULT '',
  agent_home TEXT NOT NULL DEFAULT '',
  first_round_skipped INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (provider, unit_key)
);
CREATE TABLE unit_marks (
  provider TEXT NOT NULL, unit_key TEXT NOT NULL,
  mark TEXT NOT NULL CHECK (mark != ''),
  value TEXT NOT NULL CHECK (value != ''),
  PRIMARY KEY (provider, unit_key, mark)
) WITHOUT ROWID;
CREATE TABLE round_days (
  day TEXT PRIMARY KEY,
  total INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE round_day_prs (
  day TEXT NOT NULL, provider TEXT NOT NULL, unit_key TEXT NOT NULL,
  rounds INTEGER NOT NULL,
  PRIMARY KEY (day, provider, unit_key)
);
CREATE TABLE round_day_notes (
  day TEXT NOT NULL, kind TEXT NOT NULL, note TEXT NOT NULL,
  PRIMARY KEY (day, kind, note)
);

-- paused holds everything; repos is a JSON list of the owner/repo held
-- while it is off.
CREATE TABLE pause (
  id INTEGER PRIMARY KEY CHECK (id = 1),
  paused INTEGER NOT NULL DEFAULT 0,
  repos TEXT NOT NULL DEFAULT '[]',
  at TEXT NOT NULL DEFAULT '',
  why TEXT NOT NULL DEFAULT ''
);
INSERT INTO pause (id) VALUES (1);

CREATE TABLE turns (
  seq INTEGER PRIMARY KEY AUTOINCREMENT,
  provider TEXT NOT NULL, unit_key TEXT NOT NULL,
  intent TEXT NOT NULL, turn_id TEXT NOT NULL, trigger TEXT NOT NULL DEFAULT '',
  guidance TEXT NOT NULL DEFAULT '', head TEXT NOT NULL DEFAULT '',
  sent_to TEXT NOT NULL DEFAULT '', session TEXT NOT NULL DEFAULT '',
  state TEXT NOT NULL,
  sent_at TEXT NOT NULL, started_at TEXT NOT NULL DEFAULT '', ended_at TEXT NOT NULL DEFAULT '',
  resume_of INTEGER NOT NULL DEFAULT 0,
  replaced_by INTEGER NOT NULL DEFAULT 0, prior_state TEXT NOT NULL DEFAULT '',
  agent TEXT NOT NULL DEFAULT '', brief_run INTEGER NOT NULL DEFAULT 0, agent_session TEXT NOT NULL DEFAULT '',
  -- written only by the usage store: '', transcript or unavailable
  usage_source TEXT NOT NULL DEFAULT '', usage_read_at TEXT NOT NULL DEFAULT '', usage_attempts INTEGER NOT NULL DEFAULT 0,
  -- the intent's inputs as one JSON object with sorted keys
  inputs TEXT NOT NULL DEFAULT '{}',
  runner TEXT NOT NULL DEFAULT '', workflow TEXT NOT NULL DEFAULT '',
  -- the role of the turn's intent (pr.review, pr.address, pr.nudge, probe, headless); '' none
  role TEXT NOT NULL DEFAULT ''
);
CREATE INDEX turns_unit ON turns(provider, unit_key, seq);
CREATE INDEX turns_sent_at ON turns(sent_at);
CREATE INDEX turns_turn_id ON turns(turn_id, seq);
CREATE INDEX turns_session ON turns(session, seq);
CREATE INDEX turns_sent_to ON turns(sent_to, seq);
CREATE INDEX turns_open ON turns(state) WHERE state IN ('sent', 'started', 'asked');

CREATE TABLE turn_records (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  turn_seq INTEGER NOT NULL DEFAULT 0,
  provider TEXT NOT NULL, unit_key TEXT NOT NULL,
  session TEXT NOT NULL DEFAULT '', agent_session TEXT NOT NULL DEFAULT '',
  head TEXT NOT NULL DEFAULT '', hash TEXT NOT NULL,
  at TEXT NOT NULL, acked INTEGER NOT NULL DEFAULT 0, record TEXT NOT NULL
);
CREATE UNIQUE INDEX turn_records_once ON turn_records(provider, unit_key, turn_seq, hash);
CREATE INDEX turn_records_unit ON turn_records(provider, unit_key);

CREATE TABLE turn_answers (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  turn_seq INTEGER NOT NULL,
  provider TEXT NOT NULL, unit_key TEXT NOT NULL,
  at TEXT NOT NULL, text TEXT NOT NULL, source TEXT NOT NULL,
  option_id TEXT NOT NULL DEFAULT '', yes INTEGER NOT NULL DEFAULT 0, no INTEGER NOT NULL DEFAULT 0,
  route TEXT NOT NULL, session TEXT NOT NULL DEFAULT '',
  message_id INTEGER NOT NULL DEFAULT 0, read_at TEXT NOT NULL DEFAULT '', noticed_at TEXT NOT NULL DEFAULT '',
  gate_id INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX turn_answers_unit ON turn_answers(provider, unit_key);

-- A session is keyed by the id the loop minted for it; session_handles keeps
-- the handles its manager gave each process of it, the open one with to_at ''.
CREATE TABLE sessions (
  session_id TEXT PRIMARY KEY,
  manager TEXT NOT NULL DEFAULT '', agent TEXT NOT NULL DEFAULT '',
  pane TEXT NOT NULL DEFAULT '', agent_session TEXT NOT NULL DEFAULT '',
  workspace_id TEXT NOT NULL DEFAULT '', worktree TEXT NOT NULL DEFAULT '', home TEXT NOT NULL DEFAULT '',
  managed INTEGER NOT NULL DEFAULT 0,
  started_at TEXT NOT NULL DEFAULT '', mcp_at TEXT NOT NULL DEFAULT '',
  idle_since TEXT NOT NULL DEFAULT '', closed_at TEXT NOT NULL DEFAULT '',
  typed_at TEXT NOT NULL DEFAULT '', typed_line TEXT NOT NULL DEFAULT '',
  user_prompt_at TEXT NOT NULL DEFAULT '', prompt_seen_at TEXT NOT NULL DEFAULT '',
  provider TEXT NOT NULL DEFAULT '', unit_key TEXT NOT NULL DEFAULT ''
);

CREATE TABLE session_handles (
  session_id TEXT NOT NULL, manager TEXT NOT NULL, handle TEXT NOT NULL,
  runtime TEXT NOT NULL DEFAULT '',
  from_at TEXT NOT NULL, to_at TEXT NOT NULL DEFAULT '',
  PRIMARY KEY (session_id, from_at)
) WITHOUT ROWID;
CREATE INDEX session_handles_handle ON session_handles(manager, handle, from_at);
CREATE UNIQUE INDEX session_handles_open ON session_handles(session_id) WHERE to_at = '';

-- A worktree in the custody of one manager, its owner; workspace_id is the
-- owner's id of it. worktree_units are the units it stands for.
CREATE TABLE worktrees (
  path TEXT PRIMARY KEY,
  repo TEXT NOT NULL DEFAULT '', manager TEXT NOT NULL, workspace_id TEXT NOT NULL DEFAULT '',
  created_by TEXT NOT NULL DEFAULT '', at TEXT NOT NULL
) WITHOUT ROWID;
CREATE TABLE worktree_units (
  path TEXT NOT NULL REFERENCES worktrees (path) ON DELETE CASCADE,
  provider TEXT NOT NULL, unit_key TEXT NOT NULL, at TEXT NOT NULL,
  PRIMARY KEY (path, provider, unit_key)
) WITHOUT ROWID;

-- at is in event.TimeLayout, which sorts as text: the newest stop and the
-- stops to drop are found by it.
CREATE TABLE turn_stops (
  session TEXT NOT NULL, prompt_id TEXT NOT NULL, agent_session TEXT NOT NULL DEFAULT '',
  at TEXT NOT NULL, message TEXT NOT NULL DEFAULT '',
  blocked INTEGER NOT NULL DEFAULT 0, record_id INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX turn_stops_session ON turn_stops(session, at);
CREATE INDEX turn_stops_at ON turn_stops(at);

CREATE TABLE view_applied (
  sink TEXT NOT NULL, provider TEXT NOT NULL, unit_key TEXT NOT NULL, role TEXT NOT NULL,
  status TEXT NOT NULL DEFAULT '', unread INTEGER NOT NULL DEFAULT 0, summary TEXT NOT NULL DEFAULT '',
  col TEXT NOT NULL DEFAULT '', override TEXT NOT NULL DEFAULT '', at TEXT NOT NULL,
  PRIMARY KEY (sink, provider, unit_key, role)
);

CREATE TABLE brief_runs (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  trigger TEXT NOT NULL, -- wanted, background, refresh, trial, extraction, fields, legacy
  intake INTEGER NOT NULL DEFAULT 0,
  version TEXT NOT NULL, attempt_of TEXT NOT NULL,
  started_at TEXT NOT NULL, ended_at TEXT NOT NULL DEFAULT '',
  input_ms INTEGER, call_ms INTEGER, agent_ms INTEGER,
  outcome TEXT NOT NULL DEFAULT 'running', -- running, ok, reused, failed, unfit, over-budget, too-long, timeout, unavailable, interrupted
  error TEXT NOT NULL DEFAULT '',
  model TEXT NOT NULL DEFAULT '', cost_usd REAL,
  input_tokens INTEGER, output_tokens INTEGER, cache_read_tokens INTEGER, cache_write_tokens INTEGER,
  input_bytes INTEGER, input_cut INTEGER NOT NULL DEFAULT 0,
  brief TEXT NOT NULL DEFAULT '', -- the brief JSON, ok runs only
  tags TEXT NOT NULL DEFAULT '', -- JSON lists: normalized key:value, what normalization refused
  dropped_tags TEXT NOT NULL DEFAULT '',
  input_sha TEXT NOT NULL DEFAULT '',
  reused_from INTEGER NOT NULL DEFAULT 0,
  provider TEXT NOT NULL DEFAULT '', unit_key TEXT NOT NULL DEFAULT '',
  -- what the unit was at when the run read it, in its provider's format
  revision TEXT NOT NULL DEFAULT '',
  -- the provider's own parts of what the run read, a JSON object
  input TEXT NOT NULL DEFAULT '{}',
  intent TEXT NOT NULL DEFAULT ''
);

-- run is the ok brief run in use and last_run the latest attempt; the
-- brief, its model, cost and times are that run's.
CREATE TABLE unit_briefs (
  provider TEXT NOT NULL, unit_key TEXT NOT NULL,
  failures INTEGER NOT NULL DEFAULT 0, attempt_of TEXT NOT NULL DEFAULT '',
  run INTEGER NOT NULL DEFAULT 0, last_run INTEGER NOT NULL DEFAULT 0,
  wanted_at TEXT NOT NULL DEFAULT '', retry_at TEXT NOT NULL DEFAULT '',
  gave_up_at TEXT NOT NULL DEFAULT '', gave_up_reason TEXT NOT NULL DEFAULT '',
  PRIMARY KEY (provider, unit_key)
);

CREATE TABLE unit_tags (
  provider TEXT NOT NULL, unit_key TEXT NOT NULL,
  key TEXT NOT NULL, value TEXT NOT NULL, run INTEGER NOT NULL,
  PRIMARY KEY (provider, unit_key, key, value)
);
CREATE INDEX unit_tags_kv ON unit_tags(key, value);

CREATE TABLE intakes (
  seq INTEGER PRIMARY KEY AUTOINCREMENT,
  provider TEXT NOT NULL, unit_key TEXT NOT NULL,
  intent TEXT NOT NULL, source TEXT NOT NULL, -- cli, cockpit, menubar, tui, session
  request TEXT NOT NULL,
  state TEXT NOT NULL, -- accepted, briefing, starting, started, failed, canceled
  brief TEXT NOT NULL DEFAULT '', -- ready, without, off
  note TEXT NOT NULL DEFAULT '',
  accepted_at TEXT NOT NULL, briefing_at TEXT NOT NULL DEFAULT '', starting_at TEXT NOT NULL DEFAULT '', ended_at TEXT NOT NULL DEFAULT '',
  turn_seq INTEGER NOT NULL DEFAULT 0, turn_id TEXT NOT NULL DEFAULT '',
  error TEXT NOT NULL DEFAULT '',
  log TEXT NOT NULL DEFAULT '[]'
);
CREATE UNIQUE INDEX intakes_open ON intakes(provider, unit_key, intent) WHERE state IN ('accepted', 'briefing', 'starting');

CREATE TABLE turn_usage (
  turn_seq INTEGER NOT NULL, model TEXT NOT NULL,
  messages INTEGER NOT NULL,
  input_tokens INTEGER NOT NULL, output_tokens INTEGER NOT NULL,
  cache_read_tokens INTEGER NOT NULL, cache_write_tokens INTEGER NOT NULL,
  cost_usd REAL,
  first_at TEXT NOT NULL, last_at TEXT NOT NULL,
  PRIMARY KEY (turn_seq, model)
);

CREATE TABLE agent_sessions (
  agent_session TEXT PRIMARY KEY,
  kind TEXT NOT NULL DEFAULT '',
  session TEXT NOT NULL DEFAULT '', home TEXT NOT NULL DEFAULT '', worktree TEXT NOT NULL DEFAULT '',
  transcript TEXT NOT NULL DEFAULT '',
  first_seen_at TEXT NOT NULL, last_seen_at TEXT NOT NULL
);

CREATE TABLE plan_items (
  id TEXT PRIMARY KEY,
  workflow TEXT NOT NULL, provider TEXT NOT NULL, unit_key TEXT NOT NULL, key_value TEXT NOT NULL,
  decision TEXT NOT NULL, reason TEXT NOT NULL DEFAULT '',
  steps TEXT NOT NULL DEFAULT '[]', why TEXT NOT NULL DEFAULT '{}',
  default_outcome TEXT NOT NULL DEFAULT '', deadline TEXT NOT NULL DEFAULT '',
  guidance TEXT NOT NULL DEFAULT '', attempts INTEGER NOT NULL DEFAULT 0, error TEXT NOT NULL DEFAULT '',
  at TEXT NOT NULL,
  gain_memory INTEGER NOT NULL DEFAULT 0, gain_disk INTEGER NOT NULL DEFAULT 0,
  saved_at TEXT NOT NULL DEFAULT '', observe INTEGER NOT NULL DEFAULT 0
) WITHOUT ROWID;
CREATE INDEX plan_items_workflow ON plan_items(workflow);

CREATE TABLE workflow_runs (
  workflow TEXT NOT NULL, provider TEXT NOT NULL, unit_key TEXT NOT NULL, key_value TEXT NOT NULL,
  status TEXT NOT NULL, attempts INTEGER NOT NULL DEFAULT 0, error TEXT NOT NULL DEFAULT '', at TEXT NOT NULL,
  PRIMARY KEY (workflow, provider, unit_key, key_value)
) WITHOUT ROWID;

CREATE TABLE workflow_cursors (
  workflow TEXT PRIMARY KEY,
  cursor INTEGER NOT NULL DEFAULT 0, last_fire TEXT NOT NULL DEFAULT ''
) WITHOUT ROWID;

CREATE TABLE classifications (
  provider TEXT NOT NULL, unit_key TEXT NOT NULL, fingerprint TEXT NOT NULL, workflow TEXT NOT NULL,
  output TEXT NOT NULL DEFAULT '{}', at TEXT NOT NULL,
  PRIMARY KEY (provider, unit_key, fingerprint, workflow)
) WITHOUT ROWID;

CREATE TABLE classify_days (
  day TEXT PRIMARY KEY,
  total INTEGER NOT NULL DEFAULT 0
) WITHOUT ROWID;

CREATE TABLE fetches (
  workspace_id TEXT PRIMARY KEY,
  at TEXT NOT NULL
) WITHOUT ROWID;

CREATE TABLE links (
  provider TEXT NOT NULL, unit_key TEXT NOT NULL,
  to_provider TEXT NOT NULL, to_key TEXT NOT NULL,
  kind TEXT NOT NULL, item TEXT NOT NULL DEFAULT '',
  evidence TEXT NOT NULL, origin TEXT NOT NULL, at TEXT NOT NULL,
  PRIMARY KEY (provider, unit_key, to_provider, to_key, kind, item)
) WITHOUT ROWID;

CREATE INDEX links_to ON links (to_provider, to_key);

-- category is the category of state, as the profile declares it; review_every (seconds) and reviewed_at are a
-- parent's review.
CREATE TABLE tasks (
  id TEXT PRIMARY KEY,
  kind TEXT NOT NULL, state TEXT NOT NULL, title TEXT NOT NULL DEFAULT '', notes TEXT NOT NULL DEFAULT '',
  parent TEXT NOT NULL DEFAULT '', position INTEGER NOT NULL DEFAULT 0,
  sequential INTEGER NOT NULL DEFAULT 0,
  snooze_until TEXT NOT NULL DEFAULT '', snooze_on_activity INTEGER NOT NULL DEFAULT 0,
  snooze_return TEXT NOT NULL DEFAULT '', snooze_mute INTEGER NOT NULL DEFAULT 0,
  waiting TEXT NOT NULL DEFAULT '',
  changed INTEGER NOT NULL DEFAULT 0, seen_at TEXT NOT NULL DEFAULT '',
  origin TEXT NOT NULL, created TEXT NOT NULL, updated TEXT NOT NULL,
  rev INTEGER NOT NULL,
  category TEXT NOT NULL DEFAULT '',
  review_every INTEGER NOT NULL DEFAULT 0, reviewed_at TEXT NOT NULL DEFAULT ''
) WITHOUT ROWID;
CREATE INDEX tasks_state ON tasks (state);
CREATE INDEX tasks_rev ON tasks (rev);
CREATE INDEX tasks_category ON tasks (category);

CREATE TABLE task_items (
  task_id TEXT NOT NULL REFERENCES tasks (id) ON DELETE CASCADE,
  provider TEXT NOT NULL, unit_key TEXT NOT NULL, item TEXT NOT NULL,
  PRIMARY KEY (task_id, provider, unit_key, item)
) WITHOUT ROWID;
CREATE INDEX task_items_unit ON task_items (provider, unit_key);

CREATE TABLE task_units (
  task_id TEXT NOT NULL REFERENCES tasks (id) ON DELETE CASCADE,
  provider TEXT NOT NULL, unit_key TEXT NOT NULL, ord INTEGER NOT NULL,
  PRIMARY KEY (task_id, provider, unit_key)
) WITHOUT ROWID;
CREATE INDEX task_units_unit ON task_units (provider, unit_key);

-- task_props.value is the property's task.Value as JSON; writer is who wrote it.
CREATE TABLE task_props (
  task_id TEXT NOT NULL REFERENCES tasks (id) ON DELETE CASCADE,
  name TEXT NOT NULL, value TEXT NOT NULL, writer TEXT NOT NULL, at TEXT NOT NULL,
  PRIMARY KEY (task_id, name)
) WITHOUT ROWID;

-- A gate's task_id is NULL while no task holds its unit. intents is a JSON list.
CREATE TABLE gates (
  id INTEGER PRIMARY KEY,
  task_id TEXT REFERENCES tasks (id),
  provider TEXT NOT NULL DEFAULT '', unit_key TEXT NOT NULL DEFAULT '',
  kind TEXT NOT NULL, name TEXT NOT NULL DEFAULT '', subject TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL, intents TEXT NOT NULL, intent TEXT NOT NULL DEFAULT '', answer TEXT NOT NULL DEFAULT '',
  by_actor TEXT NOT NULL DEFAULT '', rule TEXT NOT NULL DEFAULT '',
  deadline TEXT NOT NULL DEFAULT '', default_intent TEXT NOT NULL DEFAULT '',
  opened_at TEXT NOT NULL, closed_at TEXT NOT NULL DEFAULT ''
);
CREATE INDEX gates_due ON gates (status, deadline);
CREATE INDEX gates_task ON gates (task_id);
CREATE INDEX gates_unit ON gates (provider, unit_key);

CREATE TABLE task_position (
  only INTEGER PRIMARY KEY CHECK (only = 1),
  epoch TEXT NOT NULL, rev INTEGER NOT NULL
);

CREATE TABLE placements (
  provider TEXT NOT NULL, unit_key TEXT NOT NULL,
  repo TEXT NOT NULL DEFAULT '', checkout TEXT NOT NULL DEFAULT '', role TEXT NOT NULL DEFAULT '',
  shared INTEGER NOT NULL DEFAULT 0,
  set_at TEXT NOT NULL,
  PRIMARY KEY (provider, unit_key)
);

-- messages.answer is the answer as the Inbox lists it; a turn's question and an agent post keep
-- theirs in turn_answers (message_id) too, and a proposal only here.
CREATE TABLE messages (
  id INTEGER PRIMARY KEY,
  task_id TEXT NOT NULL DEFAULT '',
  provider TEXT NOT NULL DEFAULT '', unit_key TEXT NOT NULL DEFAULT '',
  kind TEXT NOT NULL, text TEXT NOT NULL,
  options TEXT NOT NULL DEFAULT '[]', recommend TEXT NOT NULL DEFAULT '', confirm INTEGER NOT NULL DEFAULT 0,
  origin_kind TEXT NOT NULL, turn_seq INTEGER NOT NULL DEFAULT 0, session TEXT NOT NULL DEFAULT '',
  workflow TEXT NOT NULL DEFAULT '', proposal INTEGER NOT NULL DEFAULT 0,
  state TEXT NOT NULL, answer TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL, closed_at TEXT NOT NULL DEFAULT ''
);
CREATE INDEX messages_open ON messages (state, id);
CREATE INDEX messages_task ON messages (task_id);
CREATE UNIQUE INDEX messages_proposal ON messages (proposal) WHERE origin_kind = 'ledger';

CREATE TABLE unit_cards (
  provider TEXT NOT NULL, unit_key TEXT NOT NULL,
  card TEXT NOT NULL, at TEXT NOT NULL,
  PRIMARY KEY (provider, unit_key)
) WITHOUT ROWID;

CREATE TABLE drafts (
  id INTEGER PRIMARY KEY,
  provider TEXT NOT NULL, unit_key TEXT NOT NULL,
  items TEXT NOT NULL DEFAULT '[]', task TEXT NOT NULL DEFAULT '',
  kind TEXT NOT NULL, target TEXT NOT NULL DEFAULT '', text TEXT NOT NULL DEFAULT '',
  reaction TEXT NOT NULL DEFAULT '', original TEXT NOT NULL DEFAULT '', edited INTEGER NOT NULL DEFAULT 0,
  origin_kind TEXT NOT NULL, turn_seq INTEGER NOT NULL DEFAULT 0, intent TEXT NOT NULL DEFAULT '',
  workflow TEXT NOT NULL DEFAULT '', rule TEXT NOT NULL DEFAULT '', rule_key TEXT NOT NULL DEFAULT '',
  approval_mode TEXT NOT NULL DEFAULT '', approval_window INTEGER NOT NULL DEFAULT 0, deadline TEXT NOT NULL DEFAULT '',
  state TEXT NOT NULL,
  evidence_ref TEXT NOT NULL DEFAULT '', evidence_permalink TEXT NOT NULL DEFAULT '', evidence_at TEXT NOT NULL DEFAULT '',
  error TEXT NOT NULL DEFAULT '', reason TEXT NOT NULL DEFAULT '',
  would_policy TEXT NOT NULL DEFAULT '', would_reason TEXT NOT NULL DEFAULT '', would_outcome TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL, updated_at TEXT NOT NULL, sending_at TEXT NOT NULL DEFAULT '',
  rev INTEGER NOT NULL DEFAULT 0,
  purpose TEXT NOT NULL DEFAULT '', delivery TEXT NOT NULL DEFAULT '', effects TEXT NOT NULL DEFAULT '[]',
  -- when the effects of a sent draft were applied; '' while they are not
  effects_applied_at TEXT NOT NULL DEFAULT '',
  -- the unit of the proposing turn when it is not the draft's; '' when it is
  origin_provider TEXT NOT NULL DEFAULT '', origin_unit_key TEXT NOT NULL DEFAULT '',
  -- an edit's: the text it replaced
  replaced TEXT NOT NULL DEFAULT ''
);
CREATE INDEX drafts_state ON drafts (state, id);
CREATE INDEX drafts_unit ON drafts (provider, unit_key, id);
CREATE INDEX drafts_task ON drafts (task, id);
CREATE INDEX drafts_rule ON drafts (rule, rule_key, id);
CREATE INDEX drafts_workflow ON drafts (workflow, provider, unit_key, id);

CREATE TABLE projects (
  task_id TEXT PRIMARY KEY REFERENCES tasks (id) ON DELETE CASCADE,
  slug TEXT NOT NULL DEFAULT ''
) WITHOUT ROWID;
CREATE UNIQUE INDEX projects_slug ON projects (slug) WHERE slug != '';

CREATE TABLE review_runs (
  id TEXT PRIMARY KEY,
  started_at TEXT NOT NULL,
  finished_at TEXT NOT NULL DEFAULT '',
  done TEXT NOT NULL DEFAULT ''
) WITHOUT ROWID;
CREATE INDEX review_runs_open ON review_runs (finished_at, started_at);

CREATE TABLE dossiers (
  owner TEXT PRIMARY KEY,
  rel_path TEXT NOT NULL, status TEXT NOT NULL,
  bytes INTEGER NOT NULL DEFAULT 0, files INTEGER NOT NULL DEFAULT 0,
  last_write_at TEXT NOT NULL DEFAULT '', resolved_at TEXT NOT NULL DEFAULT '',
  archived_at TEXT NOT NULL DEFAULT '', purge_after TEXT NOT NULL DEFAULT ''
) WITHOUT ROWID;

-- A dossier directory a merge of tasks moved in the store and not yet on disk: paths under the dossiers
-- root, an archive at the same path under .archive with .tar.gz. Moves apply in seq order, a later one
-- moving what an earlier one put in place.
CREATE TABLE dossier_moves (
  seq INTEGER PRIMARY KEY,
  from_path TEXT NOT NULL,
  to_path TEXT NOT NULL
);

CREATE TABLE items (
  provider TEXT NOT NULL, unit_key TEXT NOT NULL, id TEXT NOT NULL,
  body TEXT NOT NULL, state TEXT NOT NULL,
  confirmed INTEGER NOT NULL DEFAULT 0, provenance TEXT NOT NULL,
  created_at TEXT NOT NULL, updated_at TEXT NOT NULL,
  PRIMARY KEY (provider, unit_key, id)
) WITHOUT ROWID;

CREATE INDEX items_state ON items (state, provider);

CREATE TABLE item_ops (
  seq INTEGER PRIMARY KEY,
  provider TEXT NOT NULL, unit_key TEXT NOT NULL, item TEXT NOT NULL,
  op TEXT NOT NULL, provenance TEXT NOT NULL, outcome TEXT NOT NULL, reason TEXT NOT NULL DEFAULT '',
  at TEXT NOT NULL
);

CREATE INDEX item_ops_unit ON item_ops (provider, unit_key, at);

CREATE TABLE item_proposals (
  id INTEGER PRIMARY KEY,
  provider TEXT NOT NULL, unit_key TEXT NOT NULL, item TEXT NOT NULL,
  op TEXT NOT NULL, provenance TEXT NOT NULL, reason TEXT NOT NULL DEFAULT '',
  dedupe TEXT NOT NULL, state TEXT NOT NULL, at TEXT NOT NULL
);

CREATE UNIQUE INDEX item_proposals_open ON item_proposals (dedupe) WHERE state = 'open';
CREATE INDEX item_proposals_state ON item_proposals (state, id);

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
