package overlay

import (
	"testing"

	dintent "github.com/wloophq/wloop/internal/domain/intent"
	"github.com/wloophq/wloop/internal/domain/record"
	"github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
)

var reviews = dintent.NewRegistry([]dintent.Def{{Name: "review", Answerable: true, DrivesRow: true}})

func askedRec(text string) record.Record {
	return record.Record{State: record.Asked, Summary: text, Question: &record.Question{Kind: "choice", Text: text,
		Options: []record.Option{{ID: "publish", Label: "Publish", Consequence: "c", Publishes: true}, {ID: "hold", Label: "Hold", Consequence: "c"}}}}
}

func askedBook() turn.Book {
	return turn.Book{Unit: unit.Unit{Provider: "github", Key: "acme/widgets#7"},
		Turns:   []turn.Turn{{Seq: 1, Intent: "review", Role: "pr.review", ID: "abc", State: turn.Asked, Session: "term_1", StartedAt: h(1)}},
		Records: []turn.Record{{ID: 1, TurnSeq: 1, Session: "term_1", At: h(2), Record: askedRec("publish?")}}}
}

func answer(b *turn.Book, route turn.Route, n int) {
	a := b.Answer(turn.Answer{TurnSeq: 1, OptionID: "publish", Source: turn.Cockpit, At: h(n)})
	a.Route = route
}

func TestAnAnswerSentHidesTheQuestion(t *testing.T) {
	for _, route := range []turn.Route{turn.Typed, turn.Hooked, turn.Resumed} {
		b := askedBook()
		answer(&b, route, 3)
		run, ok := RunOf(b, reviews)
		if !ok || run.Question != nil {
			t.Errorf("%s: the answered question still shows: %+v", route, run.Question)
		}
		if s, ok := run.Turn.Situation(false); !ok || s.Kind != KindLoopWorking || s.Reason != "your answer was sent" {
			t.Errorf("%s: situation %+v %v", route, s, ok)
		}
	}
}

func TestAQueuedAnswerShowsTheQuestionAsQueued(t *testing.T) {
	b := askedBook()
	answer(&b, turn.Queued, 3)
	run, ok := RunOf(b, reviews)
	if !ok || run.Question == nil || !run.Question.Queued || run.Question.Open() {
		t.Fatalf("question %+v", run.Question)
	}
	if s, ok := run.Turn.Situation(false); !ok || s.Kind != KindLoopWorking || s.Reason != "your answer is queued until the agent's session is free" {
		t.Fatalf("situation %+v %v", s, ok)
	}
}

func TestARefusedOrPendingAnswerLeavesTheQuestionOpen(t *testing.T) {
	for _, route := range []turn.Route{turn.Refused, turn.Pending} {
		b := askedBook()
		answer(&b, route, 3)
		run, _ := RunOf(b, reviews)
		if run.Question == nil || !run.Question.Open() {
			t.Errorf("%s: question %+v", route, run.Question)
		}
	}
}

func TestALaterQuestionOfTheTurnShowsAgain(t *testing.T) {
	b := askedBook()
	answer(&b, turn.Typed, 3)
	b.Records = append(b.Records, turn.Record{ID: 2, TurnSeq: 1, Session: "term_1", At: h(4), Record: askedRec("delete the draft?")})
	run, _ := RunOf(b, reviews)
	if q := run.Question; q == nil || !q.Open() || q.RecordID != 2 || q.Text != "delete the draft?" || len(q.Options) != 2 {
		t.Fatalf("question %+v", q)
	}
	if s, _ := run.Turn.Situation(false); s.Kind != KindLoopWaiting {
		t.Fatalf("situation %+v", s)
	}
}

// TestAQuestionCarriesItsConfirmPin serves the draft a confirm's yes approves on the row's question, so a client
// knows a yes approves a draft it does not show.
func TestAQuestionCarriesItsConfirmPin(t *testing.T) {
	pin := &record.DraftRef{ID: 42, Rev: 3}
	q, ok := QuestionOf(&turn.Turn{State: turn.Asked}, &turn.Record{ID: 1, Record: record.Record{Question: &record.Question{Kind: "confirm", Text: "Post it?", Draft: pin}}})
	if !ok || q.Draft == nil || *q.Draft != *pin || q.Draft == pin {
		t.Fatalf("question %+v, want a copy of the confirm's pin", q)
	}
}
