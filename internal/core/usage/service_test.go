package usage

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/usage"
)

var now = time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)

type clock struct{}

func (clock) Now() time.Time { return now }

type saved struct {
	rows   []usage.Model
	source string
	evs    []port.Event
}

type store struct {
	pending    []Turn
	endedSince time.Time
	limit      int
	saved      map[int64]saved
	attempted  []int64
}

func (s *store) Pending(_ context.Context, endedSince time.Time, limit int) ([]Turn, error) {
	s.endedSince, s.limit = endedSince, limit
	return s.pending, nil
}

func (s *store) SaveUsage(_ context.Context, seq int64, rows []usage.Model, source string, _ time.Time, evs ...port.Event) error {
	if s.saved == nil {
		s.saved = map[int64]saved{}
	}
	s.saved[seq] = saved{rows, source, evs}
	return nil
}

func (s *store) Attempted(_ context.Context, seq int64) error {
	s.attempted = append(s.attempted, seq)
	return nil
}

type reader struct {
	rows []usage.Model
	err  error
	got  []Turn
}

func (r *reader) Usage(_ context.Context, t Turn) ([]usage.Model, error) {
	r.got = append(r.got, t)
	return r.rows, r.err
}

func turnOf(seq int64, kind, session string, endedAgo time.Duration) Turn {
	return Turn{
		Seq: seq, PR: pr.Ref{Owner: "o", Repo: "r", Number: 1}, Kind: kind, AgentSession: session,
		From: now.Add(-endedAgo - time.Minute), To: now.Add(-endedAgo),
	}
}

func run(t *testing.T, s Service) string {
	t.Helper()
	got, err := s.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestNoReaderIsUnavailable(t *testing.T) {
	st := &store{pending: []Turn{turnOf(1, "gone", "a1", time.Minute)}}
	got := run(t, Service{Store: st, ReaderFor: func(string) Reader { return nil }, Clock: clock{}})
	if s := st.saved[1]; s.source != SourceUnavailable || len(s.rows) != 0 {
		t.Fatalf("saved = %+v, want unavailable with no rows", s)
	}
	if ev := st.saved[1].evs; len(ev) != 1 || ev[0].Kind != KindTurnUsage || ev[0].Key != "1" || ev[0].Data != SourceUnavailable {
		t.Fatalf("events = %+v", ev)
	}
	if got != "usage: 0 read, 1 unavailable, 0 retried" {
		t.Fatalf("summary = %q", got)
	}
	if !st.endedSince.Equal(now.Add(-Window)) || st.limit != Batch {
		t.Fatalf("Pending(%v, %d)", st.endedSince, st.limit)
	}
}

func TestNoAgentSessionWaitsAnHour(t *testing.T) {
	r := &reader{}
	st := &store{pending: []Turn{
		turnOf(1, "k", "", Late-time.Minute),
		turnOf(2, "k", "", Late+time.Minute),
	}}
	got := run(t, Service{Store: st, ReaderFor: func(string) Reader { return r }, Clock: clock{}})
	if _, ok := st.saved[1]; ok {
		t.Fatal("a turn ended under an hour ago without an agent session was saved")
	}
	if st.saved[2].source != SourceUnavailable {
		t.Fatalf("saved[2] = %+v, want unavailable", st.saved[2])
	}
	if len(r.got) != 0 {
		t.Fatalf("the reader was asked %d times without an agent session", len(r.got))
	}
	if got != "usage: 0 read, 1 unavailable, 0 retried" {
		t.Fatalf("summary = %q", got)
	}
}

func TestReaderErrorRetries(t *testing.T) {
	broken := &reader{err: errors.New("disk")}
	gone := &reader{err: ErrNoTranscript}
	rows := []usage.Model{{Model: "m", Messages: 1, Tokens: usage.Tokens{Output: 3}}}
	ok := &reader{rows: rows}
	st := &store{pending: []Turn{turnOf(1, "broken", "a", time.Minute), turnOf(2, "gone", "b", time.Minute), turnOf(3, "ok", "c", time.Minute)}}
	readers := map[string]Reader{"broken": broken, "gone": gone, "ok": ok}
	got := run(t, Service{Store: st, ReaderFor: func(k string) Reader { return readers[k] }, Clock: clock{}})
	if _, saved := st.saved[1]; saved {
		t.Fatal("a turn whose reader failed was saved")
	}
	if st.saved[2].source != SourceUnavailable {
		t.Fatalf("saved[2] = %+v, want unavailable", st.saved[2])
	}
	if s := st.saved[3]; s.source != SourceTranscript || len(s.rows) != 1 || s.rows[0].Tokens.Output != 3 {
		t.Fatalf("saved[3] = %+v", s)
	}
	if got != "usage: 1 read, 1 unavailable, 1 retried" {
		t.Fatalf("summary = %q", got)
	}
	if !slices.Equal(st.attempted, []int64{1}) {
		t.Fatalf("attempted %v", st.attempted)
	}
}

func TestAReadThatKeepsFailingGivesUp(t *testing.T) {
	broken := &reader{err: errors.New("disk")}
	last, early := turnOf(1, "k", "a", time.Minute), turnOf(2, "k", "b", time.Minute)
	last.Attempts, early.Attempts = MaxAttempts-1, MaxAttempts-2
	st := &store{pending: []Turn{last, early}}
	var logs []string
	got := run(t, Service{Store: st, ReaderFor: func(string) Reader { return broken }, Clock: clock{},
		Log: func(f string, a ...any) { logs = append(logs, fmt.Sprintf(f, a...)) }})
	if st.saved[1].source != SourceUnavailable || len(st.saved[1].rows) != 0 {
		t.Fatalf("saved[1] = %+v, want unavailable after %d attempts", st.saved[1], MaxAttempts)
	}
	if _, saved := st.saved[2]; saved || !slices.Equal(st.attempted, []int64{2}) {
		t.Fatalf("saved %v attempted %v", st.saved, st.attempted)
	}
	if got != "usage: 0 read, 1 unavailable, 1 retried" {
		t.Fatalf("summary = %q", got)
	}
	if !slices.Equal(logs, []string{"usage: turn 1 of o/r#1: disk"}) {
		t.Fatalf("logs %q, want the first error only", logs)
	}
}

func TestReaderIsPickedByTheTurnsKind(t *testing.T) {
	configured, echo := &reader{}, &reader{}
	st := &store{pending: []Turn{turnOf(1, "echo", "a", time.Minute)}}
	readerFor := func(k string) Reader {
		if k == "echo" {
			return echo
		}
		return configured
	}
	run(t, Service{Store: st, ReaderFor: readerFor, Clock: clock{}})
	if len(echo.got) != 1 || len(configured.got) != 0 {
		t.Fatalf("echo reader asked %d times, configured agent's reader %d", len(echo.got), len(configured.got))
	}
	if echo.got[0].Seq != 1 {
		t.Fatalf("reader got %+v", echo.got[0])
	}
}
