package sqlite

import (
	"context"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/core/turns"
	"github.com/wloophq/wloop/internal/domain/record"
	"github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/meter"
	"github.com/wloophq/wloop/internal/meter/metertest"
)

func bookOfFive(t *testing.T, s *Store) turn.Book {
	t.Helper()
	var b turn.Book
	b.Unit = u7
	for i := range 5 {
		b.Turns = append(b.Turns, turn.Turn{ID: fmt.Sprintf("r%d", i), State: turn.Sent, SentAt: noteAt, Inputs: map[string]any{"n": float64(i)}})
	}
	saved, err := s.Save(context.Background(), b)
	if err != nil {
		t.Fatal(err)
	}
	for i := range 10 {
		saved.Records = append(saved.Records, turn.Record{TurnSeq: saved.Turns[i/2].Seq, Hash: fmt.Sprintf("h%d", i), At: noteAt, Record: record.Record{Summary: "s"}})
	}
	if saved, err = s.Save(context.Background(), saved); err != nil {
		t.Fatal(err)
	}
	return saved
}

func TestSaveOneTurnChange(t *testing.T) {
	t.Parallel()
	s := newStore(t)
	b := bookOfFive(t, s)
	b.Turns[2].State = turn.Started
	b.Turns[2].StartedAt = noteAt.Add(time.Minute)
	ctx, sc := metertest.Start(t, context.Background())
	saved, err := s.Save(ctx, b)
	if err != nil {
		t.Fatal(err)
	}
	if got := metertest.Count(sc, metertest.Limit{Res: meter.SQL, Op: "exec"}); got != 1 {
		t.Errorf("%d writes, want 1\n%s", got, metertest.Report(sc.Tally()))
	}
	if got := metertest.Count(sc, metertest.Limit{Res: meter.SQL, Op: "query"}); got != 3 {
		t.Errorf("%d reads, want 3\n%s", got, metertest.Report(sc.Tally()))
	}
	reloaded, _, err := s.BookOf(context.Background(), turns.Key{Turn: "r2"})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(saved, reloaded) {
		t.Errorf("returned book differs from a reload:\n%+v\n%+v", saved, reloaded)
	}
}

func TestSaveReturnsAssignedIDs(t *testing.T) {
	t.Parallel()
	s := newStore(t)
	b := bookOfFive(t, s)
	newTurn := turn.Turn{ID: "r5", State: turn.Sent, SentAt: noteAt}
	b.Turns = append(b.Turns, newTurn)
	b.Records = append(b.Records, turn.Record{Hash: "new", At: noteAt}, turn.Record{TurnSeq: b.Turns[0].Seq, Hash: "h0", At: noteAt})
	b.Answers = append(b.Answers, turn.Answer{Text: "yes", At: noteAt, Source: turn.Source("cli")})
	saved, err := s.Save(context.Background(), b)
	if err != nil {
		t.Fatal(err)
	}
	reloaded, _, err := s.BookOf(context.Background(), turns.Key{Turn: "r5"})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(saved, reloaded) {
		t.Errorf("returned book differs from a reload:\n%+v\n%+v", saved, reloaded)
	}
	last := saved.Turns[len(saved.Turns)-1]
	if last.Seq == 0 || len(saved.Records) != 11 || saved.Answers[0].ID == 0 || saved.Answers[0].TurnSeq != last.Seq {
		t.Errorf("assigned ids missing: %+v", saved)
	}
}

func TestSaveNoChangeEmitsNoChange(t *testing.T) {
	t.Parallel()
	e, log := openNoted(t)
	s := New(e.db)
	b := bookOfFive(t, s)
	_, n, err := noted(log, func() error {
		_, err := s.Save(context.Background(), b)
		return err
	})
	if err != nil || n != 0 {
		t.Fatalf("unchanged save: %d notes, err %v, want none", n, err)
	}
	ctx, sc := metertest.Start(t, context.Background())
	if _, err := s.Save(ctx, b); err != nil {
		t.Fatal(err)
	}
	if got := metertest.Count(sc, metertest.Limit{Res: meter.SQL, Op: "exec"}); got != 0 {
		t.Errorf("%d writes, want 0", got)
	}
}

func TestSaveRekeyedRecordFreesItsOldKey(t *testing.T) {
	t.Parallel()
	s := newStore(t)
	b := bookOfFive(t, s)
	seq := b.Turns[0].Seq
	b.Records[0].Hash = "moved"
	b.Records = append(b.Records,
		turn.Record{TurnSeq: seq, Hash: "moved", At: noteAt},
		turn.Record{TurnSeq: seq, Hash: "h0", At: noteAt})
	saved, err := s.Save(context.Background(), b)
	if err != nil {
		t.Fatal(err)
	}
	reloaded, _, err := s.BookOf(context.Background(), turns.Key{Turn: "r0"})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(saved, reloaded) {
		t.Errorf("returned book differs from a reload:\n%+v\n%+v", saved, reloaded)
	}
}

func TestAnAnswerKeepsItsMessageAndReadState(t *testing.T) {
	t.Parallel()
	s := newStore(t)
	ctx := context.Background()
	b := bookOfFive(t, s)
	if _, err := s.db.w.ExecContext(ctx, `INSERT INTO messages (id, provider, unit_key, kind, text, origin_kind, turn_seq, created_at)
		VALUES (41, ?, ?, 'agent_post', 'q', 'agent', ?, 'x'), (42, ?, ?, 'question', 'q', 'turn', ?, 'x')`,
		string(u7.Provider), u7.Key, b.Turns[0].Seq, string(u7.Provider), u7.Key, b.Turns[0].Seq); err != nil {
		t.Fatal(err)
	}
	read := noteAt.Add(time.Hour)
	b.Answers = append(b.Answers,
		turn.Answer{TurnSeq: b.Turns[0].Seq, Text: "post", Source: turn.Cockpit, Route: turn.Typed, At: noteAt, Message: 41, Posted: true, NoticedAt: noteAt.Add(time.Minute), ReadAt: read},
		turn.Answer{TurnSeq: b.Turns[0].Seq, Text: "question", Source: turn.Cockpit, Route: turn.Queued, At: noteAt, Message: 42})
	saved, err := s.Save(ctx, b)
	if err != nil {
		t.Fatal(err)
	}
	reloaded, _, err := s.BookOf(ctx, turns.Key{Turn: "r0"})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(saved.Answers, reloaded.Answers) {
		t.Fatalf("returned answers differ from a reload:\n%+v\n%+v", saved.Answers, reloaded.Answers)
	}
	reloaded.Answers[1].ReadAt = read
	if saved, err = s.Save(ctx, reloaded); err != nil {
		t.Fatal(err)
	}
	if again, _, err := s.BookOf(ctx, turns.Key{Turn: "r0"}); err != nil || !again.Answers[1].ReadAt.Equal(read) || again.Answers[1].Posted {
		t.Fatalf("read state not saved: %+v %v", again.Answers, err)
	}
}
