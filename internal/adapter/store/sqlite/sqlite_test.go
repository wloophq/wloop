package sqlite

import (
	"context"
	"database/sql"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/domain/round"
)

var golden = []string{
	"agent_sessions: agent_session, kind, session, home, worktree, transcript, first_seen_at, last_seen_at",
	"brief_runs: id, trigger, intake, version, attempt_of, started_at, ended_at, input_ms, call_ms, agent_ms, outcome, error, model, cost_usd, input_tokens, output_tokens, cache_read_tokens, cache_write_tokens, input_bytes, input_cut, brief, tags, dropped_tags, input_sha, reused_from, provider, unit_key, revision, input, intent",
	"classifications: provider, unit_key, fingerprint, workflow, output, at",
	"classify_days: day, total",
	"dossier_moves: seq, from_path, to_path",
	"dossiers: owner, rel_path, status, bytes, files, last_write_at, resolved_at, archived_at, purge_after",
	"drafts: id, provider, unit_key, items, task, kind, target, text, reaction, original, edited, origin_kind, turn_seq, intent, workflow, rule, rule_key, state, evidence_ref, evidence_permalink, evidence_at, error, reason, created_at, updated_at, sending_at, rev, purpose, delivery, effects, effects_applied_at, origin_provider, origin_unit_key, replaced",
	"events: id, provider, unit_key, kind, key, actor, at, data, anchor",
	"extraction_cursors: provider, unit_key, last_ts, last_seq, verdict, verdict_why, viewer_role, topic, started_by_viewer, closure_posted, watched, at, closure_marked",
	"extraction_days: day, calls, cost_usd",
	"extraction_hints: provider, unit_key, item_id, mode, context, why",
	"extraction_runs: id, provider, unit_key, at, model, cost_usd, outcome, error",
	"fetches: workspace_id, at",
	"gates: id, task_id, provider, unit_key, kind, name, subject, status, intents, intent, answer, by_actor, rule, deadline, default_intent, opened_at, closed_at",
	"instruction_parts: sha, body",
	"intakes: seq, provider, unit_key, intent, source, request, state, brief, note, accepted_at, briefing_at, starting_at, ended_at, turn_seq, turn_id, error, log",
	"item_ops: seq, provider, unit_key, item, op, provenance, outcome, reason, at",
	"item_proposals: id, provider, unit_key, item, op, provenance, reason, dedupe, state, at",
	"items: provider, unit_key, id, body, state, confirmed, provenance, created_at, updated_at",
	"links: provider, unit_key, to_provider, to_key, kind, item, evidence, origin, at",
	"messages: id, task_id, provider, unit_key, kind, text, options, recommend, confirm, origin_kind, turn_seq, session, workflow, proposal, state, answer, created_at, closed_at",
	"meta: key, value",
	"overrides: provider, unit_key, muted_until, mute_until_state, hold, taken_over, muted_kind, muted_waiting_on, next_guidance, tracked",
	"pause: id, paused, repos, at, why",
	"placements: provider, unit_key, repo, checkout, role, shared, set_at",
	"plan_items: id, workflow, provider, unit_key, key_value, decision, reason, steps, why, default_outcome, deadline, guidance, attempts, error, at, gain_memory, gain_disk, saved_at, observe",
	"pr_details: owner, repo, number, version, read_at, body",
	"pr_seen: owner, repo, number, at",
	"pull_requests: owner, repo, number, title, author, state, is_draft, head_oid, head_branch, base_branch, is_fork, checks_state, review_decision, review_requests, url, created_at, updated_at, merged_at, closed_at, body_edited_at, changed_files, reasons, ws_id, ws_name, ws_status, ws_review, ws_agent, ws_comment, ws_main, ws_manager, failing_checks, mergeable, mergeable_at",
	"review_runs: id, started_at, finished_at, done",
	"round_day_notes: day, kind, note",
	"round_day_prs: day, provider, unit_key, rounds",
	"round_days: day, total",
	"round_state: provider, unit_key, sent_at, agent_home",
	"segment_map: provider, conversation, segment_id, team, from_ts, to_ts, created_at, updated_at, closed",
	"session_handles: session_id, manager, handle, runtime, from_at, to_at",
	"sessions: session_id, manager, agent, pane, agent_session, workspace_id, worktree, home, managed, started_at, mcp_at, idle_since, closed_at, typed_at, typed_line, user_prompt_at, prompt_seen_at, provider, unit_key",
	"slack_changes: seq, conversation, ts, thread_ts, kind, edited_ts, at",
	"slack_conversations: id, team, name, kind, is_member, archived, dm_user, hwm, admitted, updated_at",
	"slack_cursors: name, value, at",
	"slack_messages: conversation, ts, thread_ts, parent_user, user, bot_id, app_id, username, subtype, text, edited_ts, deleted, files, links, mentions, permalink, via, received_at",
	"slack_objects: object, conversation, ts, kind, version, latest_reply, reply_count, watched, reasons, last_activity_at, next_check_at, seen_at, admitted_at",
	"slack_reactions: conversation, ts, name, user",
	"slack_users: id, name, display_name, email, is_bot, deleted, fetched_at",
	"task_items: task_id, provider, unit_key, item",
	"task_position: only, epoch, rev",
	"task_props: task_id, name, value, writer, at",
	"task_units: task_id, provider, unit_key, ord",
	"tasks: id, kind, state, title, notes, parent, position, sequential, snooze_until, snooze_on_activity, snooze_return, snooze_mute, waiting, changed, seen_at, origin, created, updated, rev, category, review_every, reviewed_at",
	"turn_answers: id, turn_seq, provider, unit_key, at, text, source, option_id, yes, no, route, session, message_id, read_at, noticed_at, gate_id",
	"turn_instructions: sha, kind, repo, created_at, bundled_sha, sources, parts",
	"turn_records: id, turn_seq, provider, unit_key, session, agent_session, head, hash, at, acked, record",
	"turn_stops: session, prompt_id, agent_session, at, message, blocked, record_id",
	"turn_usage: turn_seq, model, messages, input_tokens, output_tokens, cache_read_tokens, cache_write_tokens, cost_usd, first_at, last_at",
	"turns: seq, provider, unit_key, intent, turn_id, trigger, guidance, head, sent_to, session, state, sent_at, started_at, ended_at, resume_of, replaced_by, prior_state, agent, brief_run, agent_session, usage_source, usage_read_at, usage_attempts, inputs, runner, workflow, role",
	"unit_briefs: provider, unit_key, failures, attempt_of, run, last_run, wanted_at, retry_at, gave_up_at, gave_up_reason",
	"unit_cards: provider, unit_key, card, at",
	"unit_marks: provider, unit_key, mark, value",
	"unit_snapshots: provider, unit_key, snapshot, rev, at",
	"view_applied: sink, provider, unit_key, role, status, unread, summary, col, override, at",
	"workflow_cursors: workflow, cursor, last_fire",
	"workflow_runs: workflow, provider, unit_key, key_value, status, attempts, error, at",
	"worktree_units: path, provider, unit_key, at",
	"worktrees: path, repo, manager, workspace_id, created_by, at",
}

func openTemp(t *testing.T) (*DB, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "state", "store.db")
	db, err := Open(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db, path
}

func tableList(t *testing.T, db *sql.DB, schema string) []string {
	t.Helper()
	rows, err := db.Query(`SELECT name FROM ` + schema + `.sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%' ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatal(err)
		}
		names = append(names, n)
	}
	rows.Close()
	var out []string
	for _, n := range names {
		cols, err := db.Query(`SELECT name FROM pragma_table_info(?, ?) ORDER BY cid`, n, schema)
		if err != nil {
			t.Fatal(err)
		}
		var cs []string
		for cols.Next() {
			var c string
			if err := cols.Scan(&c); err != nil {
				t.Fatal(err)
			}
			cs = append(cs, c)
		}
		cols.Close()
		out = append(out, n+": "+strings.Join(cs, ", "))
	}
	return out
}

func TestSchemaIsOneVersion(t *testing.T) {
	t.Parallel()
	db, _ := openTemp(t)
	if got := tableList(t, db.r, "main"); !slices.Equal(got, golden) {
		t.Fatalf("tables of a new store:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(golden, "\n"))
	}
	var v int
	if err := db.R().QueryRowContext(context.Background(), `PRAGMA user_version`).Scan(&v); err != nil || v != SchemaVersion {
		t.Fatalf("user_version %d %v", v, err)
	}
}

func TestOpenKeepsAStoreOfThisVersion(t *testing.T) {
	t.Parallel()
	db, path := openTemp(t)
	if _, err := execW(db, `INSERT INTO meta (key, value) VALUES ('k', 'v')`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	again, err := Open(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer again.Close()
	var v string
	if err := again.R().QueryRowContext(context.Background(), `SELECT value FROM meta WHERE key = 'k'`).Scan(&v); err != nil || v != "v" {
		t.Fatalf("%q %v", v, err)
	}
	if err := Check(path); err != nil {
		t.Fatalf("Check on a current store: %v", err)
	}
}

func openErr(path string) error {
	db, err := Open(path, nil)
	if err == nil {
		db.Close()
	}
	return err
}

func TestRoundsSaveWritesOnlyChangedFields(t *testing.T) {
	t.Parallel()
	db, _ := openTemp(t)
	ctx := context.Background()
	r := NewRounds(db)
	now := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	key := "acme/api#7"
	a, err := r.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	b, err := r.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	a.SetHeads(key, "abc", "def")
	b.RecordSent(key, round.Reviewed, "fp", now)
	b.SetAgentHome(key, "/home/me/.claude")
	if err := r.Save(ctx, a); err != nil {
		t.Fatal(err)
	}
	if err := r.Save(ctx, b); err != nil {
		t.Fatal(err)
	}
	c, err := r.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	p := c.PRs[key]
	if p.Marks[round.AddressedChecks] != "abc" || p.Marks[round.AddressedConflict] != "def" || p.Marks[round.Reviewed] != "fp" || p.AgentHome != "/home/me/.claude" || !p.SentAt.Equal(now) {
		t.Fatalf("%+v", p)
	}
	if d := c.Days[round.DayKey(now)]; d.Total != 1 || d.PerPR[key] != 1 {
		t.Fatalf("day %+v", d)
	}

	c.Count(key, now)
	c.SetMark(key, round.AddressedConflict, "")
	c.FirstCapHit("acme/api#8", now)
	other, _ := r.Load(ctx)
	other.Count("acme/web#1", now)
	for _, st := range []*round.Store{c, other} {
		if err := r.Save(ctx, st); err != nil {
			t.Fatal(err)
		}
	}
	if len(c.Changes().PRs)+len(c.Changes().Marks)+len(c.Changes().Counts)+len(c.Changes().Notes) != 0 {
		t.Fatal("a saved store kept its changes")
	}
	d, _ := r.Load(ctx)
	day := d.Days[round.DayKey(now)]
	if day.Total != 3 || day.PerPR[key] != 2 || day.PerPR["acme/web#1"] != 1 || !slices.Equal(day.CapLogged, []string{"acme/api#8"}) || d.PRs[key].Marks[round.Reviewed] != "fp" || d.PRs[key].Marks[round.AddressedConflict] != "" {
		t.Fatalf("day %+v, pr %+v", day, d.PRs[key])
	}

	tomorrow := now.Add(24 * time.Hour)
	d.FirstNote("hello", tomorrow)
	if err := r.Save(ctx, d); err != nil {
		t.Fatal(err)
	}
	e, _ := r.Load(ctx)
	if _, ok := e.Days[round.DayKey(now)]; ok || len(e.Days) != 1 {
		t.Fatalf("days before the newest kept: %+v", e.Days)
	}
}

func TestRoundsSaveRefusesAKeyThatDoesNotParse(t *testing.T) {
	t.Parallel()
	db, _ := openTemp(t)
	r := NewRounds(db)
	st := round.New()
	st.SetMark("not a key", round.Reviewed, "x")
	if err := r.Save(context.Background(), st); err == nil {
		t.Fatal("saved a key that does not parse")
	}
}

func TestPauseLeavesAnEvent(t *testing.T) {
	t.Parallel()
	db, _ := openTemp(t)
	ctx := context.Background()
	r := NewRounds(db)
	if p, err := r.Pause(ctx); err != nil || p.On || p.Repos == nil {
		t.Fatalf("a new store: %+v %v", p, err)
	}
	at := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	want := round.Pause{Repos: []string{"acme/sandbox"}, At: at, Why: "rehearsal"}
	if err := r.SetPause(ctx, want); err != nil {
		t.Fatal(err)
	}
	if err := r.SetPause(ctx, round.Pause{At: at.Add(time.Minute)}); err != nil {
		t.Fatal(err)
	}
	if err := r.SetPause(ctx, want); err != nil {
		t.Fatal(err)
	}
	got, err := r.Pause(ctx)
	if err != nil || got.On || !slices.Equal(got.Repos, want.Repos) || !got.At.Equal(at) || got.Why != "rehearsal" {
		t.Fatalf("%+v %v", got, err)
	}
	rows, err := db.R().QueryContext(context.Background(), `SELECT provider, unit_key, kind, at, data FROM events ORDER BY at, id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var events []string
	for rows.Next() {
		var provider, key, kind, at, data string
		if err := rows.Scan(&provider, &key, &kind, &at, &data); err != nil {
			t.Fatal(err)
		}
		events = append(events, strings.Join([]string{provider + key, kind, at, data}, "|"))
	}
	wantEvents := []string{
		"|paused|2026-10-17T12:00:00.000000000Z|on acme/sandbox; rehearsal",
		"|paused|2026-10-17T12:00:00.000000001Z|on acme/sandbox; rehearsal",
		"|resumed|2026-10-17T12:01:00.000000000Z|",
	}
	if !slices.Equal(events, wantEvents) {
		t.Fatalf("events:\n%s", strings.Join(events, "\n"))
	}
}

func TestParseKey(t *testing.T) {
	t.Parallel()
	for key, ok := range map[string]bool{"acme/api#7": true, "acme/api": false, "acme#7": false, "acme/a/b#7": false, "/api#7": false, "acme/api#0": false, "acme/api#x": false} {
		if _, err := ParseKey(key); (err == nil) != ok {
			t.Errorf("%q: %v", key, err)
		}
	}
}

func rowsOf(t *testing.T, db *DB, query string, args ...any) []string {
	t.Helper()
	rows, err := db.R().QueryContext(context.Background(), query, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	cols, _ := rows.Columns()
	var out []string
	for rows.Next() {
		vals := make([]sql.NullString, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			t.Fatal(err)
		}
		var parts []string
		for _, v := range vals {
			if v.Valid {
				parts = append(parts, v.String)
			} else {
				parts = append(parts, "NULL")
			}
		}
		out = append(out, strings.Join(parts, "|"))
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}
