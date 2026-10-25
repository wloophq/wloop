package sqlite

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/domain/event"
	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/workflow"
	"github.com/wloophq/wloop/internal/meter"
	"github.com/wloophq/wloop/internal/meter/metertest"
)

const day = 24 * time.Hour

var (
	retainNow = time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	// past is older than every 30-day window, recent inside all of them.
	past   = retainNow.Add(-31 * day)
	recent = retainNow.Add(-29 * day)
)

func at(t time.Time) string { return t.UTC().Format(event.TimeLayout) }

func exec(t *testing.T, db *DB, q string, args ...any) {
	t.Helper()
	if _, err := execW(db, q, args...); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
}

func countOf(t *testing.T, db *DB, q string, args ...any) int {
	t.Helper()
	var n int
	if err := db.R().QueryRowContext(context.Background(), q, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	return n
}

func retain(t *testing.T, db *DB, p Retention) Retained {
	t.Helper()
	got, err := db.Retain(context.Background(), retainNow, p)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func event_(t *testing.T, db *DB, provider, unitKey, kind, key string, when time.Time) {
	t.Helper()
	exec(t, db, `INSERT INTO events (provider, unit_key, kind, key, at) VALUES (?, ?, ?, ?, ?)`, provider, unitKey, kind, key, at(when))
}

func remainingEvents(t *testing.T, db *DB) []string {
	t.Helper()
	rows, err := db.R().QueryContext(context.Background(), `SELECT provider || '|' || unit_key || '|' || kind || '|' || key FROM events ORDER BY 1`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatal(err)
		}
		out = append(out, s)
	}
	return out
}

func TestRetainSessionEvents(t *testing.T) {
	t.Parallel()
	db, _ := openTemp(t)
	for _, k := range SessionEventKinds {
		event_(t, db, "session", "term_1", k, "term_1:"+k, past)
		event_(t, db, "", "", k, "", past)
	}
	event_(t, db, "", "", KindPaused, "", past)
	event_(t, db, "github", "acme/api#7", "session_idle", "term_1:x", past)
	event_(t, db, "session", "term_2", "session_idle", "term_2:recent", recent)
	event_(t, db, "session", "term_1", "turn_stopped", "term_1:p1", past)

	got := retain(t, db, Retention{SessionEvents: 30 * day})
	if got[SessionEventsKey] != 2*len(SessionEventKinds) || len(got) != 1 {
		t.Errorf("retained %v, want session events %d", got, 2*len(SessionEventKinds))
	}
	want := []string{"github|acme/api#7|session_idle|term_1:x", "session|term_1|turn_stopped|term_1:p1", "session|term_2|session_idle|term_2:recent", "||paused|"}
	if left := remainingEvents(t, db); !slices.Equal(left, want) {
		t.Errorf("events left %q, want %q", left, want)
	}
}

func TestRetainViewEvents(t *testing.T) {
	t.Parallel()
	db, _ := openTemp(t)
	for _, k := range []string{"view_applied", "view_override", "view_sink_failed"} {
		event_(t, db, "github", "acme/api#7", k, "", past)
		event_(t, db, "github", "acme/api#7", k, "", recent)
	}
	event_(t, db, "github", "acme/api#7", "review", "r1", past)

	if got := retain(t, db, Retention{ViewEvents: 30 * day}); got[ViewEventsKey] != 3 || len(got) != 1 {
		t.Errorf("retained %v, want view events 3", got)
	}
	if n := countOf(t, db, `SELECT count(*) FROM events WHERE at < ?`, at(recent)); n != 1 {
		t.Errorf("%d old events left, want the review alone", n)
	}
}

func TestRetainClosedSessions(t *testing.T) {
	t.Parallel()
	db, _ := openTemp(t)
	cut := retainNow.Add(-30 * day)
	for handle, closed := range map[string]string{
		"old": formatTime(past), "recent": formatTime(recent), "open": "", "at-cutoff": formatTime(cut),
		"old-named": formatTime(past), "old-sent-to": formatTime(past),
	} {
		exec(t, db, `INSERT INTO sessions (session_id, started_at, closed_at) VALUES (?, ?, ?)`, handle, formatTime(past.Add(-day)), closed)
	}
	exec(t, db, `INSERT INTO turns (provider, unit_key, intent, turn_id, state, sent_at, session) VALUES ('github', 'acme/api#7', 'review', 'r1', 'asked', ?, 'old-named')`, at(past))
	exec(t, db, `INSERT INTO turns (provider, unit_key, intent, turn_id, state, sent_at, sent_to) VALUES ('github', 'acme/api#7', 'review', 'r2', 'sent', ?, 'old-sent-to')`, at(past))
	exec(t, db, `INSERT INTO turns (provider, unit_key, intent, turn_id, state, sent_at, session, agent_session, usage_source) VALUES ('github', 'acme/api#7', 'review', 'r3', 'done', ?, 'old', 'read', 'transcript')`, at(past))
	exec(t, db, `INSERT INTO turns (provider, unit_key, intent, turn_id, state, sent_at, session, agent_session) VALUES ('github', 'acme/api#7', 'review', 'r4', 'done', ?, 'old', 'unread')`, at(past))
	for _, u := range []string{"old", "open", "gone"} {
		exec(t, db, `INSERT INTO unit_marks (provider, unit_key, mark, value) VALUES ('session', ?, 'w/m', 'v')`, u)
	}
	for id, seen := range map[string]time.Time{"read": past, "unread": past, "no-turn": past, "seen-recently": recent} {
		exec(t, db, `INSERT INTO agent_sessions (agent_session, first_seen_at, last_seen_at) VALUES (?, ?, ?)`, id, at(seen), at(seen))
	}

	got := retain(t, db, Retention{ClosedSessions: 30 * day})
	if want := (Retained{"sessions": 1, "agent_sessions": 2, "unit_marks": 2}); !maps.Equal(got, want) {
		t.Errorf("retained %v, want %v", got, want)
	}
	if left := column(t, db, `SELECT session_id FROM sessions ORDER BY session_id`); !slices.Equal(left, []string{"at-cutoff", "old-named", "old-sent-to", "open", "recent"}) {
		t.Errorf("sessions left %q", left)
	}
	if left := column(t, db, `SELECT agent_session FROM agent_sessions ORDER BY 1`); !slices.Equal(left, []string{"seen-recently", "unread"}) {
		t.Errorf("agent sessions left %q", left)
	}
	if left := column(t, db, `SELECT unit_key FROM unit_marks`); !slices.Equal(left, []string{"open"}) {
		t.Errorf("session marks left %q", left)
	}
}

func column(t *testing.T, db *DB, q string, args ...any) []string {
	t.Helper()
	rows, err := db.R().QueryContext(context.Background(), q, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatal(err)
		}
		out = append(out, s)
	}
	return out
}

func TestRetainStops(t *testing.T) {
	t.Parallel()
	db, _ := openTemp(t)
	for i, when := range []time.Time{past, recent} {
		exec(t, db, `INSERT INTO turn_stops (session, prompt_id, at) VALUES ('term_1', ?, ?)`, fmt.Sprint("p", i), at(when))
	}
	if got := retain(t, db, Retention{Stops: 30 * day}); got["turn_stops"] != 1 {
		t.Errorf("retained %v, want one stop", got)
	}
	if left := column(t, db, `SELECT prompt_id FROM turn_stops`); !slices.Equal(left, []string{"p1"}) {
		t.Errorf("stops left %q", left)
	}
}

func insertPR(t *testing.T, db *DB, ref pr.Ref, state string, merged, closed string) {
	t.Helper()
	exec(t, db, `INSERT INTO pull_requests (owner, repo, number, state, created_at, merged_at, closed_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		ref.Owner, ref.Repo, ref.Number, state, at(time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)), merged, closed)
}

func TestRetainViewRows(t *testing.T) {
	t.Parallel()
	db, _ := openTemp(t)
	merged, closed, open := pr.Ref{Owner: "acme", Repo: "api", Number: 1}, pr.Ref{Owner: "acme", Repo: "api", Number: 2}, pr.Ref{Owner: "acme", Repo: "api", Number: 3}
	insertPR(t, db, merged, "MERGED", at(past), at(past))
	insertPR(t, db, closed, "CLOSED", "", at(recent))
	insertPR(t, db, open, "OPEN", "", "")
	for _, ref := range []pr.Ref{merged, closed, open} {
		for _, role := range []string{"review", "author"} {
			exec(t, db, `INSERT INTO view_applied (sink, provider, unit_key, role, at) VALUES ('orca', 'github', ?, ?, ?)`, ref.String(), role, at(past))
		}
	}

	if got := retain(t, db, Retention{ViewRows: 30 * day}); got["view_applied"] != 2 || len(got) != 1 {
		t.Errorf("retained %v, want the merged pull request's two rows", got)
	}
	if left := column(t, db, `SELECT DISTINCT unit_key FROM view_applied ORDER BY 1`); !slices.Equal(left, []string{closed.String(), open.String()}) {
		t.Errorf("view rows left for %q", left)
	}
}

func TestRetainBriefBodies(t *testing.T) {
	t.Parallel()
	db, _ := openTemp(t)
	run := func(id int, started time.Time) {
		exec(t, db, `INSERT INTO brief_runs (id, owner, repo, number, trigger, version, attempt_of, started_at, ended_at, outcome, input_tokens, brief, files, tags, dropped_tags)
			VALUES (?, 'acme', 'api', 1, 'wanted', 'v1', 'h', ?, ?, 'ok', 120, '{"goal":"g"}', '["a.go"]', '["change:fix"]', '["x:y"]')`, id, at(started), at(started.Add(time.Minute)))
	}
	for id := 1; id <= 4; id++ {
		run(id, retainNow.Add(-8*day))
	}
	run(5, retainNow.Add(-6*day))
	exec(t, db, `INSERT INTO pr_briefs (owner, repo, number, run, last_run) VALUES ('acme', 'api', 1, 1, 2)`)
	exec(t, db, `INSERT INTO turns (provider, unit_key, intent, turn_id, state, sent_at, brief_run) VALUES ('github', 'acme/api#1', 'review', 'r1', 'done', ?, 3)`, at(past))

	if got := retain(t, db, Retention{BriefBodies: 7 * day}); got["brief_runs"] != 1 || len(got) != 1 {
		t.Errorf("retained %v, want one brief run compacted", got)
	}
	if left := column(t, db, `SELECT id FROM brief_runs WHERE brief != '' AND files != '' ORDER BY id`); !slices.Equal(left, []string{"1", "2", "3", "5"}) {
		t.Errorf("runs with a body %q, want 1 2 3 5", left)
	}
	var outcome, ended, tags, dropped string
	var tokens int
	if err := db.R().QueryRowContext(context.Background(), `SELECT outcome, ended_at, input_tokens, tags, dropped_tags FROM brief_runs WHERE id = 4`).Scan(&outcome, &ended, &tokens, &tags, &dropped); err != nil {
		t.Fatal(err)
	}
	if outcome != "ok" || ended == "" || tokens != 120 || tags == "" || dropped == "" {
		t.Errorf("the compacted run lost more than its body: %q %q %d %q %q", outcome, ended, tokens, tags, dropped)
	}
}

// prTables counts one pull request's rows in every table it has rows in.
var prTables = map[string]string{
	"pull_requests": `SELECT count(*) FROM pull_requests WHERE owner = ? AND repo = ? AND number = ?`,
	"pr_details":    `SELECT count(*) FROM pr_details WHERE owner = ? AND repo = ? AND number = ?`,
	"pr_seen":       `SELECT count(*) FROM pr_seen WHERE owner = ? AND repo = ? AND number = ?`,
	"pr_briefs":     `SELECT count(*) FROM pr_briefs WHERE owner = ? AND repo = ? AND number = ?`,
	"brief_runs":    `SELECT count(*) FROM brief_runs WHERE owner = ? AND repo = ? AND number = ?`,
	"pr_tags":       `SELECT count(*) FROM pr_tags WHERE owner = ? AND repo = ? AND number = ?`,
	"events":        `SELECT count(*) FROM events WHERE provider = 'github' AND unit_key = ?`,
	"overrides":     `SELECT count(*) FROM overrides WHERE provider = 'github' AND unit_key = ?`,
	"round_state":   `SELECT count(*) FROM round_state WHERE provider = 'github' AND unit_key = ?`,
	"unit_marks":    `SELECT count(*) FROM unit_marks WHERE provider = 'github' AND unit_key = ?`,
	"view_applied":  `SELECT count(*) FROM view_applied WHERE provider = 'github' AND unit_key = ?`,
	"turns":         `SELECT count(*) FROM turns WHERE provider = 'github' AND unit_key = ?`,
	"turn_records":  `SELECT count(*) FROM turn_records WHERE provider = 'github' AND unit_key = ?`,
	"turn_answers":  `SELECT count(*) FROM turn_answers WHERE provider = 'github' AND unit_key = ?`,
	"intakes":       `SELECT count(*) FROM intakes WHERE provider = 'github' AND unit_key = ?`,
	"turn_usage":    `SELECT count(*) FROM turn_usage WHERE turn_seq IN (SELECT seq FROM turns WHERE provider = 'github' AND unit_key = ?)`,
	"workflow_runs": `SELECT count(*) FROM workflow_runs WHERE provider = 'github' AND unit_key = ?`,
	"plan_items":    `SELECT count(*) FROM plan_items WHERE provider = 'github' AND unit_key = ?`,
}

func prRows(t *testing.T, db *DB, ref pr.Ref) map[string]int {
	t.Helper()
	out := map[string]int{}
	for table, q := range prTables {
		args := []any{ref.String()}
		if strings.Contains(q, "owner = ?") {
			args = []any{ref.Owner, ref.Repo, ref.Number}
		}
		out[table] = countOf(t, db, q, args...)
	}
	return out
}

// seedPRRows gives ref one row in every table of prTables.
func seedPRRows(t *testing.T, db *DB, ref pr.Ref, seq int) {
	t.Helper()
	u, when := ref.String(), at(time.Date(2020, 1, 2, 0, 0, 0, 0, time.UTC))
	triple := []any{ref.Owner, ref.Repo, ref.Number}
	exec(t, db, `INSERT INTO pr_details (owner, repo, number, version, read_at, body) VALUES (?, ?, ?, 1, ?, '{}')`, append(triple, when)...)
	exec(t, db, `INSERT INTO pr_seen (owner, repo, number, at) VALUES (?, ?, ?, ?)`, append(triple, when)...)
	exec(t, db, `INSERT INTO pr_briefs (owner, repo, number) VALUES (?, ?, ?)`, triple...)
	exec(t, db, `INSERT INTO brief_runs (owner, repo, number, trigger, version, attempt_of, started_at) VALUES (?, ?, ?, 'wanted', 'v1', 'h', ?)`, append(triple, when)...)
	exec(t, db, `INSERT INTO pr_tags (owner, repo, number, key, value, run) VALUES (?, ?, ?, 'change', 'fix', 1)`, triple...)
	exec(t, db, `INSERT INTO events (provider, unit_key, kind, key, at) VALUES ('github', ?, 'review', 'r1', ?)`, u, when)
	exec(t, db, `INSERT INTO overrides (provider, unit_key, hold) VALUES ('github', ?, 1)`, u)
	exec(t, db, `INSERT INTO round_state (provider, unit_key, agent_home) VALUES ('github', ?, 'f')`, u)
	exec(t, db, `INSERT INTO unit_marks (provider, unit_key, mark, value) VALUES ('github', ?, 'reviewed', 'f')`, u)
	exec(t, db, `INSERT INTO view_applied (sink, provider, unit_key, role, at) VALUES ('orca', 'github', ?, 'review', ?)`, u, when)
	exec(t, db, `INSERT INTO turns (seq, provider, unit_key, intent, turn_id, state, sent_at) VALUES (?, 'github', ?, 'review', 'r1', 'done', ?)`, seq, u, when)
	exec(t, db, `INSERT INTO turn_records (turn_seq, provider, unit_key, hash, at, record) VALUES (?, 'github', ?, 'h', ?, '{}')`, seq, u, when)
	exec(t, db, `INSERT INTO turn_answers (turn_seq, provider, unit_key, at, text, source, route) VALUES (?, 'github', ?, ?, 'yes', 'tui', 'typed')`, seq, u, when)
	exec(t, db, `INSERT INTO intakes (provider, unit_key, intent, source, request, state, accepted_at) VALUES ('github', ?, 'review', 'cli', '{}', 'started', ?)`, u, when)
	exec(t, db, `INSERT INTO turn_usage (turn_seq, model, messages, input_tokens, output_tokens, cache_read_tokens, cache_write_tokens, first_at, last_at) VALUES (?, 'opus', 1, 1, 1, 1, 1, ?, ?)`, seq, when, when)
	exec(t, db, `INSERT INTO workflow_runs (workflow, provider, unit_key, key_value, status, at) VALUES ('review-follow-up', 'github', ?, 'k', 'dismissed', ?)`, u, when)
	exec(t, db, `INSERT INTO plan_items (id, workflow, provider, unit_key, key_value, decision, mode, at, saved_at) VALUES (?, 'address', 'github', ?, 'k', 'blocked', 'apply', ?, ?)`, "address/"+u, u, when, when)
}

func TestRetainEndedPR(t *testing.T) {
	t.Parallel()
	db, _ := openTemp(t)
	gone, kept, open := pr.Ref{Owner: "acme", Repo: "api", Number: 1}, pr.Ref{Owner: "acme", Repo: "api", Number: 2}, pr.Ref{Owner: "acme", Repo: "web", Number: 1}
	insertPR(t, db, gone, "MERGED", at(retainNow.Add(-401*day)), at(retainNow.Add(-401*day)))
	insertPR(t, db, kept, "CLOSED", "", at(retainNow.Add(-399*day)))
	insertPR(t, db, open, "OPEN", "", "")
	for i, ref := range []pr.Ref{gone, kept, open} {
		seedPRRows(t, db, ref, i+1)
	}
	event_(t, db, "session", "term_1", "turn_stopped", "term_1:p", retainNow.Add(-500*day))

	ctx, sc := metertest.Start(t, context.Background())
	got, err := db.Retain(ctx, retainNow, Retention{EndedPRs: 400 * day})
	if err != nil {
		t.Fatal(err)
	}
	if n := metertest.Count(sc, metertest.Limit{Res: meter.Tx}); n != 1 {
		t.Errorf("%d transactions, want 1\n%s", n, metertest.Report(sc.Tally()))
	}
	if n, want := metertest.Count(sc, metertest.Limit{Res: meter.SQL}), int64(1+len(endedTables)+1); n != want {
		t.Errorf("a pass that removes an ended pull request issued %d statements, want the SELECT, one DELETE per table and optimize: %d\n%s", n, want, metertest.Report(sc.Tally()))
	}
	if len(got) != len(prTables) {
		t.Errorf("retained %v, want one row in each of %d tables", got, len(prTables))
	}
	for table, n := range prRows(t, db, gone) {
		if n != 0 {
			t.Errorf("%s keeps %d rows of the pull request ended 401 days ago", table, n)
		}
	}
	for _, ref := range []pr.Ref{kept, open} {
		for table, n := range prRows(t, db, ref) {
			if n != 1 {
				t.Errorf("%s: %s has %d rows, want 1", ref, table, n)
			}
		}
	}
	if n := countOf(t, db, `SELECT count(*) FROM events WHERE provider = 'session'`); n != 1 {
		t.Errorf("%d session events, want 1", n)
	}
}

// seedEveryKind leaves one row past every window of DefaultRetention.
func seedEveryKind(t *testing.T, db *DB) {
	t.Helper()
	event_(t, db, "session", "term_1", "session_idle", "term_1:a", past)
	event_(t, db, "github", "acme/api#9", "view_applied", "", past)
	exec(t, db, `INSERT INTO sessions (session_id, closed_at) VALUES ('term_1', ?)`, formatTime(past))
	exec(t, db, `INSERT INTO agent_sessions (agent_session, first_seen_at, last_seen_at) VALUES ('a1', ?, ?)`, at(past), at(past))
	exec(t, db, `INSERT INTO turn_stops (session, prompt_id, at) VALUES ('term_1', 'p', ?)`, at(past))
	exec(t, db, `INSERT INTO instruction_parts (sha, body) VALUES ('orphan', x'00')`)
	exec(t, db, `INSERT INTO turn_instructions (sha, kind, repo, created_at, parts) VALUES ('old', 'review', 'acme/api', ?, '["orphan"]')`, at(past))
	exec(t, db, `INSERT INTO brief_runs (owner, repo, number, trigger, version, attempt_of, started_at, brief) VALUES ('acme', 'api', 9, 'wanted', 'v', 'h', ?, '{}')`, at(past))
	merged := pr.Ref{Owner: "acme", Repo: "api", Number: 9}
	insertPR(t, db, merged, "MERGED", at(past), at(past))
	exec(t, db, `INSERT INTO view_applied (sink, provider, unit_key, role, at) VALUES ('orca', 'github', ?, 'review', ?)`, merged.String(), at(past))
	ended := pr.Ref{Owner: "acme", Repo: "old", Number: 1}
	insertPR(t, db, ended, "CLOSED", "", at(retainNow.Add(-401*day)))
	seedPRRows(t, db, ended, 100)
	exec(t, db, `INSERT INTO agent_sessions (agent_session, first_seen_at, last_seen_at) VALUES ('held-by-ended', ?, ?)`, at(past), at(past))
	exec(t, db, `INSERT INTO turns (seq, provider, unit_key, intent, turn_id, state, sent_at, agent_session) VALUES (101, 'github', ?, 'review', 'r2', 'done', ?, 'held-by-ended')`, ended.String(), at(past))
	exec(t, db, `INSERT INTO sessions (session_id, closed_at) VALUES ('term_held', ?)`, formatTime(past))
	exec(t, db, `INSERT INTO turns (seq, provider, unit_key, intent, turn_id, state, sent_at, sent_to) VALUES (102, 'github', ?, 'review', 'r3', 'sent', ?, 'term_held')`, ended.String(), at(past))
}

func TestRetainIdempotent(t *testing.T) {
	t.Parallel()
	e, log := openNoted(t)
	seedEveryKind(t, e.db)
	first := retain(t, e.db, DefaultRetention)
	for _, table := range []string{SessionEventsKey, ViewEventsKey, "events", "sessions", "agent_sessions", "turn_stops", "turn_instructions", "instruction_parts", "brief_runs", "view_applied", "pull_requests"} {
		if first[table] == 0 {
			t.Errorf("the first pass left %s: %v", table, first)
		}
	}
	if n := countOf(t, e.db, `SELECT count(*) FROM agent_sessions WHERE agent_session = 'held-by-ended'`) + countOf(t, e.db, `SELECT count(*) FROM sessions WHERE session_id = 'term_held'`); n != 0 {
		t.Errorf("the first pass kept %d rows only an ended pull request's turns held", n)
	}
	got, n, err := noted(log, func() error {
		second, err := e.db.Retain(context.Background(), retainNow, DefaultRetention)
		if len(second) != 0 {
			t.Errorf("the second pass retained %v", second)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 || !got.Empty() {
		t.Errorf("the second pass noted %d changes: %+v", n, got)
	}
}

func TestRetainChange(t *testing.T) {
	t.Parallel()
	e, log := openNoted(t)
	event_(t, e.db, "session", "term_1", "session_idle", "term_1:a", past)
	exec(t, e.db, `INSERT INTO turn_stops (session, prompt_id, at) VALUES ('term_1', 'p', ?)`, at(past))
	got, n, err := noted(log, func() error {
		_, err := e.db.Retain(context.Background(), retainNow, DefaultRetention)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 || !got.All || got.Topics != port.TopicLoopEvents|port.TopicSessions {
		t.Errorf("noted %d: all %v topics %b, want one note of all with %b", n, got.All, got.Topics, port.TopicLoopEvents|port.TopicSessions)
	}
}

func TestRetainFreesPages(t *testing.T) {
	t.Parallel()
	db, _ := openTemp(t)
	data := strings.Repeat("x", 2048)
	_, err := db.Write(context.Background(), func(tx *Tx) error {
		for i := range 2000 {
			if _, err := tx.ExecContext(context.Background(), `INSERT INTO events (provider, unit_key, kind, key, at, data) VALUES ('session', 'term_1', 'session_idle', ?, ?, ?)`,
				fmt.Sprint("term_1:", i), at(past), data); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	freelist := func() int { return countOf(t, db, `PRAGMA freelist_count`) }
	noVacuum := DefaultRetention
	noVacuum.VacuumPages = 0
	if got := retain(t, db, noVacuum); got[SessionEventsKey] != 2000 {
		t.Fatalf("retained %v, want 2000 events", got)
	}
	freed := freelist()
	if freed < 1000 {
		t.Fatalf("deleting 2000 events freed %d pages", freed)
	}
	retain(t, db, DefaultRetention)
	if after := freelist(); after >= freed || after >= DefaultRetention.VacuumPages {
		t.Errorf("freelist_count %d after the pass, %d before", after, freed)
	}
}

func TestRetainedString(t *testing.T) {
	t.Parallel()
	if got := (Retained{}).String(); got != "nothing" {
		t.Errorf("empty: %q", got)
	}
	if got := (Retained{"sessions": 1, "events": 3}).String(); got != "events 3, sessions 1" {
		t.Errorf("got %q", got)
	}
}

func TestAnEventKeepsItsActor(t *testing.T) {
	t.Parallel()
	db, _ := openTemp(t)
	u := unit.Unit{Provider: unit.GitHub, Key: "acme/api#7"}
	at := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	if err := New(db).RecordEvents(context.Background(),
		port.Event{Unit: u, Kind: workflow.KindStepRan, Key: "close/session/term_1/a:0", Actor: "close", Data: "closed term_1", At: at},
		port.Event{Unit: u, Kind: "comment", Actor: "rita", At: at}); err != nil {
		t.Fatal(err)
	}
	if got := rowsOf(t, db, `SELECT kind || '|' || actor FROM events ORDER BY kind`); !slices.Equal(got, []string{"comment|rita", workflow.KindStepRan + "|close"}) {
		t.Errorf("events %q", got)
	}
}
