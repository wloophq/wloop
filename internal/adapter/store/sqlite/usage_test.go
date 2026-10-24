package sqlite

import (
	"context"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/core/usage"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/turn"
	dusage "github.com/wloophq/wloop/internal/domain/usage"
)

var (
	usageRef = pr.Ref{Owner: "acme", Repo: "api", Number: 7}
	usageAt  = time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
)

func hours(n int) time.Time { return usageAt.Add(time.Duration(n) * time.Hour) }

func saveTurns(t *testing.T, s *Store, ts ...turn.Turn) turn.Book {
	t.Helper()
	b, err := s.Save(context.Background(), turn.Book{Unit: usageRef.Unit(), Turns: ts})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func usageEvent(seq int64, source string) port.Event {
	return port.Event{Unit: usageRef.Unit(), Kind: usage.KindTurnUsage, Key: strconv.FormatInt(seq, 10), Data: source, At: usageAt}
}

func TestTurnAgentRoundTrip(t *testing.T) {
	s := newStore(t)
	b := saveTurns(t, s, turn.Turn{Intent: turn.Intent(dintent.Review), ID: "r1", State: turn.Sent, SentAt: hours(-2), Agent: "echo", BriefRun: 7})
	b.Turns[0].State, b.Turns[0].StartedAt, b.Turns[0].AgentSession = turn.Started, hours(-1), "a1"
	b, err := s.Save(context.Background(), b)
	if err != nil {
		t.Fatal(err)
	}
	got := b.Turns[0]
	if got.Agent != "echo" || got.BriefRun != 7 || got.AgentSession != "a1" {
		t.Fatalf("%+v", got)
	}
}

func TestBookSaveKeepsTheUsageColumns(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	b := saveTurns(t, s, turn.Turn{Intent: turn.Intent(dintent.Review), ID: "r1", State: turn.Done, SentAt: hours(-2), EndedAt: hours(-1), Agent: "claude", AgentSession: "a1"})
	seq := b.Turns[0].Seq
	rows := []dusage.Model{{Model: "opus", Messages: 3, Tokens: dusage.Tokens{Input: 10, Output: 20, CacheRead: 30, CacheWrite: 40}, First: hours(-2), Last: hours(-1)}}
	if err := s.db.Usage().SaveUsage(ctx, seq, rows, usage.SourceTranscript, usageAt, usageEvent(seq, usage.SourceTranscript)); err != nil {
		t.Fatal(err)
	}
	b.Turns[0].Guidance = "again"
	if _, err := s.Save(ctx, b); err != nil {
		t.Fatal(err)
	}
	var source, readAt string
	if err := s.db.R().QueryRowContext(context.Background(), `SELECT usage_source, usage_read_at FROM turns WHERE seq = ?`, seq).Scan(&source, &readAt); err != nil ||
		source != "transcript" || readAt != "2026-10-18T12:00:00.000000000Z" {
		t.Fatalf("%q %q %v", source, readAt, err)
	}
	got := rowsOf(t, s.db, `SELECT turn_seq, model, messages, input_tokens, output_tokens, cache_read_tokens, cache_write_tokens, cost_usd, first_at, last_at FROM turn_usage`)
	if !slices.Equal(got, []string{strconv.FormatInt(seq, 10) + "|opus|3|10|20|30|40|NULL|2026-10-18T10:00:00.000000000Z|2026-10-18T11:00:00.000000000Z"}) {
		t.Fatalf("turn_usage %v", got)
	}
	if evs := rowsOf(t, s.db, `SELECT kind, key, data FROM events WHERE kind = 'turn_usage'`); !slices.Equal(evs, []string{"turn_usage|" + strconv.FormatInt(seq, 10) + "|transcript"}) {
		t.Fatalf("events %v", evs)
	}
}

func TestAgentSessionUpsertKeepsTheTranscript(t *testing.T) {
	db, _ := openTemp(t)
	ctx := context.Background()
	if err := db.SaveAgentSession(ctx, AgentSession{ID: "a1", Kind: "claude", Handle: "term_1", Home: "/h", Worktree: "/w", Transcript: "/h/projects/-w/a1.jsonl", FirstSeen: hours(-3), LastSeen: hours(-2)}); err != nil {
		t.Fatal(err)
	}
	if err := db.SaveAgentSession(ctx, AgentSession{ID: "a1", Handle: "term_2", LastSeen: hours(-1)}); err != nil {
		t.Fatal(err)
	}
	if err := db.SaveAgentSession(ctx, AgentSession{ID: "a1", FirstSeen: hours(-4)}); err != nil {
		t.Fatal(err)
	}
	got := rowsOf(t, db, `SELECT agent_session, kind, session, home, worktree, transcript, first_seen_at, last_seen_at FROM agent_sessions`)
	want := "a1|claude|term_2|/h|/w|/h/projects/-w/a1.jsonl|2026-10-18T08:00:00.000000000Z|2026-10-18T11:00:00.000000000Z"
	if !slices.Equal(got, []string{want}) {
		t.Fatalf("%v", got)
	}
	if err := db.SaveAgentSession(ctx, AgentSession{ID: "a2"}); err == nil {
		t.Fatal("saved an agent session with no seen time")
	}
}

func TestAgentTranscriptReadsWhatTheHooksStored(t *testing.T) {
	db, _ := openTemp(t)
	ctx := context.Background()
	if err := db.SaveAgentSession(ctx, AgentSession{ID: "a1", Kind: "claude", Transcript: "/h/projects/-w/a1.jsonl", FirstSeen: hours(-1)}); err != nil {
		t.Fatal(err)
	}
	if path, kind, err := db.AgentTranscript(ctx, "a1"); err != nil || path != "/h/projects/-w/a1.jsonl" || kind != "claude" {
		t.Fatalf("a1: %q %q %v", path, kind, err)
	}
	if path, kind, err := db.AgentTranscript(ctx, "a9"); err != nil || path != "" || kind != "" {
		t.Fatalf("a9: %q %q %v", path, kind, err)
	}
}

func TestPendingJoinsTheAgentSession(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	b := saveTurns(t, s,
		turn.Turn{Intent: turn.Intent(dintent.Review), ID: "r1", State: turn.Done, SentAt: hours(-5), StartedAt: hours(-4), EndedAt: hours(-3), Agent: "claude", AgentSession: "a1"},
		turn.Turn{Intent: turn.Intent(dintent.Review), ID: "r2", State: turn.Unrecorded, SentAt: hours(-2), EndedAt: hours(-1), Agent: "echo"},
		turn.Turn{Intent: turn.Intent(dintent.Review), ID: "r3", State: turn.Started, SentAt: hours(-1), StartedAt: hours(-1), Agent: "claude", AgentSession: "a1"},
		turn.Turn{Intent: turn.Intent(dintent.Review), ID: "r4", State: turn.Done, SentAt: hours(-50), EndedAt: hours(-49), Agent: "claude"},
		turn.Turn{Intent: turn.Intent(dintent.Review), ID: "r5", State: turn.Withdrawn, SentAt: hours(-2), EndedAt: hours(-2), Agent: "claude"},
		turn.Turn{Intent: turn.Intent(dintent.Review), ID: "r6", State: turn.Done, SentAt: hours(-2), EndedAt: hours(-2).Add(500 * time.Millisecond), Agent: "claude"},
	)
	if err := s.db.SaveAgentSession(ctx, AgentSession{ID: "a1", Kind: "claude", Home: "/h", Worktree: "/w", Transcript: "/t/a1.jsonl", FirstSeen: hours(-4)}); err != nil {
		t.Fatal(err)
	}
	read := b.Turns[5].Seq
	if err := s.db.Usage().SaveUsage(ctx, read, nil, usage.SourceTranscript, usageAt, usageEvent(read, usage.SourceTranscript)); err != nil {
		t.Fatal(err)
	}
	got, err := s.db.Usage().Pending(ctx, hours(-24), 10)
	if err != nil {
		t.Fatal(err)
	}
	want := []usage.Turn{
		{Seq: b.Turns[0].Seq, PR: usageRef, Kind: "claude", AgentSession: "a1", Transcript: "/t/a1.jsonl", Home: "/h", Worktree: "/w", From: hours(-4), To: hours(-3)},
		{Seq: b.Turns[1].Seq, PR: usageRef, Kind: "echo", From: hours(-2), To: hours(-1)},
	}
	if !slices.Equal(got, want) {
		t.Fatalf("pending:\n%+v\nwant:\n%+v", got, want)
	}
	if one, err := s.db.Usage().Pending(ctx, hours(-24), 1); err != nil || len(one) != 1 || one[0].Seq != b.Turns[0].Seq {
		t.Fatalf("limit 1: %+v %v", one, err)
	}
}

func TestUnavailableLeavesNoUsageRow(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	b := saveTurns(t, s, turn.Turn{Intent: turn.Intent(dintent.Review), ID: "r1", State: turn.Done, SentAt: hours(-2), EndedAt: hours(-1), Agent: "echo"})
	seq := b.Turns[0].Seq
	if err := s.db.Usage().SaveUsage(ctx, seq, nil, usage.SourceUnavailable, usageAt, usageEvent(seq, usage.SourceUnavailable)); err != nil {
		t.Fatal(err)
	}
	if got := rowsOf(t, s.db, `SELECT count(*) FROM turn_usage`); !slices.Equal(got, []string{"0"}) {
		t.Fatalf("turn_usage rows %v", got)
	}
	if got := rowsOf(t, s.db, `SELECT usage_source FROM turns`); !slices.Equal(got, []string{"unavailable"}) {
		t.Fatalf("source %v", got)
	}
	if p, err := s.db.Usage().Pending(ctx, hours(-24), 10); err != nil || len(p) != 0 {
		t.Fatalf("still pending: %+v %v", p, err)
	}
	if err := s.db.Usage().SaveUsage(ctx, 999, nil, usage.SourceUnavailable, usageAt); err == nil || !strings.Contains(err.Error(), "no turn 999") {
		t.Fatalf("an unknown turn: %v", err)
	}
}

func TestPendingPutsFailedReadsLast(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	b := saveTurns(t, s,
		turn.Turn{Intent: turn.Intent(dintent.Review), ID: "r1", State: turn.Done, SentAt: hours(-5), EndedAt: hours(-4), Agent: "claude", AgentSession: "a1"},
		turn.Turn{Intent: turn.Intent(dintent.Review), ID: "r2", State: turn.Done, SentAt: hours(-3), EndedAt: hours(-2), Agent: "claude", AgentSession: "a2"},
	)
	old, newer := b.Turns[0].Seq, b.Turns[1].Seq
	for range 2 {
		if err := s.db.Usage().Attempted(ctx, old); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.db.Usage().Pending(ctx, hours(-24), 10)
	if err != nil || len(got) != 2 || got[0].Seq != newer || got[0].Attempts != 0 || got[1].Seq != old || got[1].Attempts != 2 {
		t.Fatalf("pending %+v %v", got, err)
	}
	b.Turns[0].Guidance = "again"
	if _, err := s.Save(ctx, b); err != nil {
		t.Fatal(err)
	}
	if got := rowsOf(t, s.db, `SELECT usage_attempts FROM turns WHERE seq = `+strconv.FormatInt(old, 10)); !slices.Equal(got, []string{"2"}) {
		t.Fatalf("a book save changed the attempts: %v", got)
	}
	if err := s.db.Usage().Attempted(ctx, 999); err == nil || !strings.Contains(err.Error(), "no turn 999") {
		t.Fatalf("an unknown turn: %v", err)
	}
}
