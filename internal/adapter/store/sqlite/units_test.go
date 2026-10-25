package sqlite

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/core/turns"
	"github.com/wloophq/wloop/internal/core/views"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/record"
	"github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/view"
)

var thread = unit.Unit{Provider: "thread", Key: "C1 thread 9"}

func TestLoopTablesStoreANonGitHubUnit(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	e, log := openNoted(t)
	s := New(e.db)
	review := turn.Intent(dintent.Review)

	got, _, err := noted(log, func() error {
		_, err := s.Save(ctx, turn.Book{Unit: thread, Turns: []turn.Turn{{Intent: review, ID: "r1", State: turn.Sent, SentAt: noteAt, SentTo: "term_9"}}},
			port.Event{Unit: thread, Kind: turns.KindTurnSent, Key: "r1", At: noteAt})
		return err
	})
	if err != nil || !slices.Contains(got.Units, thread) || got.Topics&(port.TopicTurns|port.TopicLoopEvents) != port.TopicTurns|port.TopicLoopEvents {
		t.Fatalf("save noted %+v: %v", got, err)
	}
	books, err := s.Books(ctx, thread)
	if err != nil || len(books[thread].Turns) != 1 {
		t.Fatalf("books %+v %v", books, err)
	}
	seq := books[thread].Turns[0].Seq
	if _, err := s.Save(ctx, turn.Book{Unit: thread,
		Records: []turn.Record{{TurnSeq: seq, Session: "term_9", Hash: "h1", At: noteAt, Record: record.Record{State: record.Asked, Summary: "q"}}},
		Answers: []turn.Answer{{TurnSeq: seq, Text: "yes", Source: turn.TUI, Route: turn.Pending, At: noteAt}}}); err != nil {
		t.Fatal(err)
	}
	b, ok, err := s.BookOf(ctx, turns.Key{Turn: "r1"})
	if err != nil || !ok || b.Unit != thread || len(b.Turns) != 1 || len(b.Records) != 1 || len(b.Answers) != 1 {
		t.Fatalf("BookOf %+v %v %v", b, ok, err)
	}
	if units, err := s.UnitsOf(ctx, []string{"term_9"}); err != nil || units["term_9"] != thread {
		t.Errorf("UnitsOf %v %v", units, err)
	}
	if open, err := s.Open(ctx); err != nil || !slices.Equal(open, []unit.Unit{thread}) {
		t.Errorf("Open %v %v", open, err)
	}
	if with, err := s.WithTurns(ctx, review); err != nil || !slices.Equal(with, []unit.Unit{thread}) {
		t.Errorf("WithTurns %v %v", with, err)
	}

	got, _, err = noted(log, func() error {
		return s.SaveApplied(ctx, "orca", view.Applied{Unit: thread, Role: view.ReviewRole, Status: view.Working, At: noteAt},
			port.Event{Unit: thread, Kind: views.KindViewApplied, At: noteAt})
	})
	if err != nil || !slices.Contains(got.Units, thread) {
		t.Fatalf("SaveApplied noted %+v: %v", got, err)
	}
	applied, err := s.Applied(ctx, "orca")
	if err != nil || len(applied) != 1 {
		t.Fatalf("Applied %v %v", applied, err)
	}
	for k, a := range applied {
		if k.Unit != thread || a.Unit != thread {
			t.Errorf("applied %+v under %+v", a, k)
		}
	}
	var kinds []string
	for _, ev := range storedEvents(t, s) {
		if ev.Unit == thread {
			kinds = append(kinds, ev.Kind)
		}
	}
	if !slices.Equal(kinds, []string{turns.KindTurnSent, views.KindViewApplied}) {
		t.Errorf("events of the thread: %v", kinds)
	}

	for _, q := range []string{
		`INSERT INTO intakes (provider, unit_key, intent, source, request, state, accepted_at) VALUES ('thread', 'C1 thread 9', 'review', 'cli', '{}', 'accepted', '2026-10-20T12:00:00.000000000Z')`,
		`INSERT INTO overrides (provider, unit_key, hold) VALUES ('thread', 'C1 thread 9', 1)`,
		`INSERT INTO round_state (provider, unit_key, agent_home) VALUES ('thread', 'C1 thread 9', 'f')`,
		`INSERT INTO unit_marks (provider, unit_key, mark, value) VALUES ('thread', 'C1 thread 9', 'reviewed', 'f')`,
		`INSERT INTO round_day_prs (day, provider, unit_key, rounds) VALUES ('2026-10-20', 'thread', 'C1 thread 9', 1)`,
	} {
		if _, err := execW(e.db, q); err != nil {
			t.Fatal(err)
		}
	}
	open, err := e.db.Intakes().Open(ctx)
	if err != nil || len(open) != 1 || open[0].Unit != (unit.Unit{Provider: "thread", Key: "C1 thread 9"}) {
		t.Errorf("open intakes %+v %v", open, err)
	}
	if _, joined, err := e.db.Intakes().Accept(ctx, newIntake(noteRef, review)); err != nil || joined {
		t.Errorf("a pull request's intake joined the thread's: %v %v", joined, err)
	}
	r := NewRounds(e.db)
	st, err := r.Load(ctx)
	if err != nil || len(st.PRs) != 0 || len(st.Days["2026-10-20"].PerPR) != 0 {
		t.Fatalf("round state of the thread read as a pull request's: %+v %v", st, err)
	}
	st.SetAgentHome(noteRef.String(), "/h")
	st.SetMark(noteRef.String(), "reviewed", "g")
	if err := r.Save(ctx, st); err != nil {
		t.Fatal(err)
	}
	var rows int
	if err := e.db.R().QueryRowContext(ctx, `SELECT count(*) FROM round_state WHERE provider = 'thread'`).Scan(&rows); err != nil || rows != 1 {
		t.Errorf("thread round_state rows %d %v", rows, err)
	}
}

func TestOpenAndWithTurnsListPullRequestsByNumber(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newStore(t)
	review := turn.Intent(dintent.Review)
	var want []unit.Unit
	for _, n := range []int{10, 9, 100} {
		u := pr.Ref{Owner: "acme", Repo: "api", Number: n}.Unit()
		if _, err := s.Save(ctx, turn.Book{Unit: u, Turns: []turn.Turn{{Intent: review, ID: fmt.Sprint("r", n), State: turn.Sent, SentAt: noteAt}}}); err != nil {
			t.Fatal(err)
		}
	}
	for _, n := range []int{9, 10, 100} {
		want = append(want, pr.Ref{Owner: "acme", Repo: "api", Number: n}.Unit())
	}
	if _, err := s.Save(ctx, turn.Book{Unit: thread, Turns: []turn.Turn{{Intent: review, ID: "rt", State: turn.Sent, SentAt: noteAt}}}); err != nil {
		t.Fatal(err)
	}
	want = append(want, thread)
	if open, err := s.Open(ctx); err != nil || !slices.Equal(open, want) {
		t.Errorf("Open %v %v, want %v", open, err, want)
	}
	if with, err := s.WithTurns(ctx, review); err != nil || !slices.Equal(with, want) {
		t.Errorf("WithTurns %v %v, want %v", with, err, want)
	}
}

func TestAnEventCitesAnotherUnit(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newStore(t)
	cited := pr.Ref{Owner: "acme", Repo: "api", Number: 7}.Unit()
	if err := s.SaveFailure(ctx, port.Event{Unit: thread, Kind: "cites", Data: cited.String(), At: noteAt}); err != nil {
		t.Fatal(err)
	}
	evs := storedEvents(t, s)
	if len(evs) != 1 || evs[0].Unit != thread {
		t.Fatalf("events %+v", evs)
	}
	provider, key, _ := strings.Cut(evs[0].Data, ":")
	got, err := unit.NewRegistry(pr.Codec{}, unit.OpaqueCodec{ID: thread.Provider}).Parse(provider, key)
	if err != nil || got != cited {
		t.Errorf("cited %v %v, want %v", got, err, cited)
	}
}

func TestAHandleKeyedEventOfNoUnitIsStoredUnderItsSession(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newStore(t)
	evs := []port.Event{
		{Kind: turns.KindSessionIdle, Key: "term_1:2026-10-20T12:00:00Z", At: noteAt},
		{Kind: turns.KindSessionClosed, Key: "term_2", At: noteAt},
		{Kind: "reconcile_report", Key: "r1", At: noteAt},
		{Unit: noteRef.Unit(), Kind: turns.KindSessionStarted, Key: "term_3:x", At: noteAt},
	}
	if err := s.SaveFailure(ctx, evs...); err != nil {
		t.Fatal(err)
	}
	want := []unit.Unit{{Provider: unit.Session, Key: "term_1"}, {Provider: unit.Session, Key: "term_2"}, {}, noteRef.Unit()}
	var got []unit.Unit
	for _, ev := range storedEvents(t, s) {
		got = append(got, ev.Unit)
	}
	if !slices.Equal(got, want) {
		t.Errorf("stored under %v, want %v", got, want)
	}
}
