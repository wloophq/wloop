package messages_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/core/messages"
	"github.com/wloophq/wloop/internal/domain/message"
	"github.com/wloophq/wloop/internal/domain/record"
	"github.com/wloophq/wloop/internal/domain/turn"
)

func answerOf(t *testing.T, r *rig, id int64, a messages.Answer) {
	t.Helper()
	a.Source = turn.Cockpit
	if _, err := r.svc.Answer(context.Background(), id, a); err != nil {
		t.Fatal(err)
	}
}

func TestReadTellsTwoPostsAnsweredInReverseOrderApart(t *testing.T) {
	r := newRig(t)
	r.started(r7)
	r.live()
	ctx := context.Background()
	opts := []record.Option{{ID: "old", Label: "Old column", Consequence: "keeps it"}, {ID: "new", Label: "New column", Consequence: "migrates"}}
	first := post(t, r, "Which column?", opts...)
	second := post(t, r, "Keep the index?")
	answerOf(t, r, second.ID, messages.Answer{Text: "drop it"})
	r.clock.advance(time.Minute)
	answerOf(t, r, first.ID, messages.Answer{OptionID: "new"})

	got, err := r.svc.Read(ctx, messages.ReadRequest{Session: "term_1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Message.ID != second.ID || got[1].Message.ID != first.ID {
		t.Fatalf("conversations %+v", got)
	}
	if a := got[0].Answers; len(a) != 1 || a[0].Text != "drop it" || a[0].OptionID != "" {
		t.Fatalf("the second post's answers %+v", a)
	}
	if a := got[1].Answers; len(a) != 1 || a[0].OptionID != "new" || len(got[1].Message.Options) != 2 {
		t.Fatalf("the first post's answers %+v of %+v", a, got[1].Message)
	}
	for _, a := range r.book(t, r7).Answers {
		if !a.ReadAt.Equal(r.clock.Now()) {
			t.Fatalf("not marked read: %+v", a)
		}
	}
	if again, err := r.svc.Read(ctx, messages.ReadRequest{Session: "term_1"}); err != nil || len(again) != 0 {
		t.Fatalf("a second read %+v %v", again, err)
	}
	one, err := r.svc.Read(ctx, messages.ReadRequest{Session: "term_1", Message: first.ID})
	if err != nil || len(one) != 1 || one[0].Message.ID != first.ID || len(one[0].Answers) != 1 {
		t.Fatalf("read of message %d: %+v %v", first.ID, one, err)
	}
}

func TestReadReturnsTheAnswerToTheTurnsQuestion(t *testing.T) {
	r := newRig(t)
	r.asked(t, r7)
	r.live()
	r.reconcile(t, r7)
	q := r.only(t)
	r.busy()
	answerOf(t, r, q.ID, messages.Answer{OptionID: "publish", Text: "shorten it"})
	if got, err := r.svc.Read(context.Background(), messages.ReadRequest{Session: "term_1"}); err != nil || len(got) != 0 {
		t.Fatalf("a queued answer was read: %+v %v", got, err)
	}
	r.sess.SetAgents()
	if err := r.turns.DeliverStopped(context.Background(), "term_1"); err != nil {
		t.Fatal(err)
	}
	got, err := r.svc.Read(context.Background(), messages.ReadRequest{Session: "term_1"})
	if err != nil || len(got) != 1 || got[0].Message.ID != q.ID || got[0].Message.Kind != message.Question {
		t.Fatalf("%+v %v", got, err)
	}
	if a := got[0].Answers; len(a) != 1 || a[0].OptionID != "publish" || a[0].Text != "shorten it" {
		t.Fatalf("answers %+v", a)
	}
}

func TestReadIsScopedByTheCallersSession(t *testing.T) {
	r := newRig(t)
	r.started(r7)
	r.live()
	m := post(t, r, "Which column?")
	answerOf(t, r, m.ID, messages.Answer{Text: "the old one"})
	r.started(r8)
	b := r.book(t, r8)
	b.Turns[0].SentTo, b.Turns[0].Session = "term_2", "term_2"
	r.books.Put(b)
	if _, err := r.svc.Read(context.Background(), messages.ReadRequest{Session: "term_2", Message: m.ID}); !errors.Is(err, messages.ErrNoMessage) {
		t.Fatalf("another session read message %d: %v", m.ID, err)
	}
	if got, err := r.svc.Read(context.Background(), messages.ReadRequest{Session: "term_2"}); err != nil || len(got) != 0 {
		t.Fatalf("another session read %+v %v", got, err)
	}
	if _, err := r.svc.Read(context.Background(), messages.ReadRequest{Session: "term_9"}); !errors.Is(err, messages.ErrNoTurn) {
		t.Fatalf("a session with no turn: %v", err)
	}
}

func TestQuestionIsTheOpenQuestionOfTheUnit(t *testing.T) {
	r := newRig(t)
	r.asked(t, r7)
	got, ok, err := r.svc.Question(context.Background(), r7)
	if err != nil || !ok || got.Kind != message.Question || got.Origin.TurnSeq != 1 {
		t.Fatalf("%+v %v %v", got, ok, err)
	}
	if _, ok, err := r.svc.Question(context.Background(), r8); err != nil || ok {
		t.Fatalf("a unit with no question: %v %v", ok, err)
	}
}

func TestConversationListsTheAnswersOfAMessage(t *testing.T) {
	r := newRig(t)
	r.started(r7)
	r.live()
	m := post(t, r, "Which column?")
	answerOf(t, r, m.ID, messages.Answer{Text: "the old one"})
	got, err := r.svc.Conversation(context.Background(), r.message(t, m.ID))
	if err != nil || len(got.Answers) != 1 || got.Answers[0].Text != "the old one" || !got.Answers[0].ReadAt.IsZero() {
		t.Fatalf("%+v %v", got, err)
	}
}

func TestANewerAnswerToTheQuestionReplacesTheQueuedOne(t *testing.T) {
	r := newRig(t)
	r.asked(t, r7)
	r.live()
	r.reconcile(t, r7)
	q := r.only(t)
	r.busy()
	answerOf(t, r, q.ID, messages.Answer{OptionID: "hold"})
	got, ok, err := r.svc.Question(context.Background(), r7)
	if err != nil || !ok || got.ID != q.ID {
		t.Fatalf("the answered question while its turn still asks: %+v %v %v", got, ok, err)
	}
	answerOf(t, r, q.ID, messages.Answer{OptionID: "publish"})
	b := r.book(t, r7)
	if len(b.Answers) != 2 || b.Answers[0].Route != turn.Refused || b.Answers[1].Route != turn.Queued || b.Answers[1].Message != q.ID {
		t.Fatalf("answers %+v", b.Answers)
	}
	if m := r.message(t, q.ID); m.State != message.Answered || m.Answer != "Publish (option publish)" {
		t.Fatalf("message %+v", m)
	}
}

func TestAQuestionWhoseTurnMovedOnTakesNoAnswer(t *testing.T) {
	r := newRig(t)
	r.asked(t, r7)
	r.live()
	r.reconcile(t, r7)
	q := r.only(t)
	answerOf(t, r, q.ID, messages.Answer{OptionID: "hold"})
	r.record(t, r7, 1, record.Record{State: record.Done, Summary: "held"})
	b := r.book(t, r7)
	b.Turns[0].State = turn.Done
	r.books.Put(b)
	if _, ok, err := r.svc.Question(context.Background(), r7); err != nil || ok {
		t.Fatalf("a done turn's question: %v %v", ok, err)
	}
	if _, err := r.svc.Answer(context.Background(), q.ID, messages.Answer{OptionID: "publish", Source: turn.Cockpit}); !errors.Is(err, messages.ErrClosed) {
		t.Fatalf("answer to a done turn's question: %v", err)
	}
}

func TestAnEarlierQuestionOfTheTurnTakesNoAnswerMeantForTheNewerOne(t *testing.T) {
	r := newRig(t)
	r.asked(t, r7)
	r.live()
	r.reconcile(t, r7)
	first := r.only(t)
	answerOf(t, r, first.ID, messages.Answer{OptionID: "hold"})
	r.record(t, r7, 1, askedRecord(&record.Question{Kind: "choice", Text: "Delete the draft?", Options: []record.Option{
		{ID: "delete", Label: "Delete", Consequence: "drops it"}, {ID: "keep", Label: "Keep", Consequence: "keeps it"}}}))
	r.reconcile(t, r7)
	second := r.only(t)
	if second.ID == first.ID {
		t.Fatal("the newer question reused the first message")
	}
	if _, err := r.svc.Answer(context.Background(), first.ID, messages.Answer{OptionID: "delete", Source: turn.Cockpit}); !errors.Is(err, messages.ErrClosed) {
		t.Fatalf("the earlier question took the answer: %v", err)
	}
	if got, ok, err := r.svc.Question(context.Background(), r7); err != nil || !ok || got.ID != second.ID {
		t.Fatalf("question %+v %v %v", got, ok, err)
	}
	if m := r.message(t, first.ID); m.Answer != "Hold (option hold)" {
		t.Fatalf("the earlier question was reworded: %+v", m)
	}
}
