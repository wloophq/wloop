package turns_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/core/turns"
	"github.com/wloophq/wloop/internal/core/turns/turnstest"
	"github.com/wloophq/wloop/internal/domain/record"
	"github.com/wloophq/wloop/internal/domain/turn"
)

// postingTurn is a started turn whose warm, capable session posted a message.
func postingTurn(t *testing.T) (*turns.Service, *turnstest.Store, *turnstest.Sessions, *recorder, *clock) {
	t.Helper()
	s, store, reg, sess, rec := newService(t)
	c := fixed(t0)
	s.Clock = c
	putTurn(store, false)
	reg.Put(turn.Session{ID: "term_1", Pane: "tab-1:leaf-1", WorkspaceID: "ws", AgentSession: "s1", StartedAt: t0.Add(-2 * time.Hour), MCPAt: t0.Add(-2 * time.Hour)})
	sess.Put("ws", turns.Terminal{Session: "term_1", Key: "tab-1", LastOutput: t0.Add(-time.Minute)})
	return s, store, sess, rec, c
}

func posted(text string) turns.AnswerRequest {
	return turns.AnswerRequest{Unit: r7, Message: 5, Turn: 1, Text: text, Source: turn.Cockpit}
}

func TestAPostedMessageAnswerIsTypedWhenIdle(t *testing.T) {
	s, store, sess, rec, _ := postingTurn(t)
	got, err := s.Answer(context.Background(), posted("use the old column"))
	if err != nil || got.Route != turn.Typed || got.Handle != "term_1" {
		t.Fatalf("%+v %v", got, err)
	}
	if typed := sess.Typed(); len(typed) != 1 || typed[0].Line != "The user answered your Inbox message: use the old column" {
		t.Fatalf("typed %+v", typed)
	}
	b := bookOf(t, store, r7)
	if len(b.Answers) != 1 || b.Answers[0].TurnSeq != 1 || b.Answers[0].Route != turn.Typed || b.Turns[0].State != turn.Started {
		t.Fatalf("book %+v %+v", b.Answers, b.Turns)
	}
	if len(rec.launches) != 0 {
		t.Fatalf("launched %+v", rec.launches)
	}
}

func TestAPostedMessageAnswerQueuesWhileBusy(t *testing.T) {
	s, store, sess, rec, c := postingTurn(t)
	ctx := context.Background()
	sess.SetAgents(working)
	got, err := s.Answer(ctx, posted("go on"))
	if err != nil || got.Route != turn.Queued {
		t.Fatalf("%+v %v", got, err)
	}
	c.advance(time.Minute)
	b := bookOf(t, store, r7)
	b.Turns[0].State = turn.Done
	b.Records = append(b.Records, turn.Record{ID: 1, TurnSeq: 1, Session: "term_1", Hash: "h", At: c.Now(), Acked: true, Record: doneRecord()})
	store.Put(b)
	sess.SetAgents()
	if err := s.DeliverStopped(ctx, "term_1"); err != nil {
		t.Fatal(err)
	}
	if typed := sess.Typed(); len(typed) != 1 || typed[0].Line != "The user answered your Inbox message: go on" {
		t.Fatalf("typed %+v", typed)
	}
	if r := routes(t, store); len(r) != 1 || r[0] != turn.Typed {
		t.Fatalf("routes %v", r)
	}
	if len(rec.launches) != 0 {
		t.Fatalf("launched %+v", rec.launches)
	}
}

func TestAPostedMessageAnswerToAGoneSessionResumesNothing(t *testing.T) {
	s, store, sess, rec, _ := postingTurn(t)
	sess.Put("ws")
	got, err := s.Answer(context.Background(), posted("go on"))
	if err != nil || got.Route != turn.Refused || got.Note == "" {
		t.Fatalf("%+v %v", got, err)
	}
	if len(rec.launches) != 0 || len(sess.Typed()) != 0 {
		t.Fatalf("launched %+v, typed %+v", rec.launches, sess.Typed())
	}
	if r := routes(t, store); len(r) != 1 || r[0] != turn.Refused {
		t.Fatalf("routes %v", r)
	}
}

func TestAQuestionAnswerLeavesAQueuedPostedAnswer(t *testing.T) {
	s, store, sess, _, c := postingTurn(t)
	ctx := context.Background()
	sess.SetAgents(working)
	if got, err := s.Answer(ctx, posted("go on")); err != nil || got.Route != turn.Queued {
		t.Fatalf("%+v %v", got, err)
	}
	c.advance(time.Minute)
	b := bookOf(t, store, r7)
	b.Turns[0].State = turn.Asked
	b.Records = append(b.Records, turn.Record{ID: 1, TurnSeq: 1, Session: "term_1", Hash: "h", At: c.Now(), Acked: true, Record: askedRecord()})
	store.Put(b)
	c.advance(time.Minute)
	if got, err := s.Answer(ctx, turns.AnswerRequest{Unit: r7, OptionID: "publish", Source: turn.Cockpit}); err != nil || got.Route != turn.Queued {
		t.Fatalf("%+v %v", got, err)
	}
	if r := routes(t, store); len(r) != 2 || r[0] != turn.Queued || r[1] != turn.Queued {
		t.Fatalf("the question's answer refused the posted one: %v", r)
	}
	sess.SetAgents()
	if err := s.DeliverStopped(ctx, "term_1"); err != nil {
		t.Fatal(err)
	}
	if typed := sess.Typed(); len(typed) != 2 {
		t.Fatalf("typed %+v", typed)
	}
}

func doneRecord() record.Record { return record.Record{State: record.Done, Summary: "done"} }

func TestAPostedMessageOfAnEndedTurnTakesNoAnswer(t *testing.T) {
	s, store, sess, _, c := postingTurn(t)
	c.advance(time.Minute)
	b := bookOf(t, store, r7)
	b.Turns[0].State = turn.Asked
	b.Records = append(b.Records, turn.Record{ID: 1, TurnSeq: 1, Session: "term_1", Hash: "h", At: c.Now(), Acked: true, Record: askedRecord()})
	store.Put(b)
	c.advance(time.Minute)
	if _, err := s.Answer(context.Background(), posted("go on")); !errors.Is(err, turn.ErrClosed) {
		t.Fatalf("answer to an asked turn's post: %v", err)
	}
	if b := bookOf(t, store, r7); len(b.Answers) != 0 || len(sess.Typed()) != 0 {
		t.Fatalf("stored %+v, typed %+v", b.Answers, sess.Typed())
	}
}
