package turns_test

import (
	"context"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/record"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/turn"
)

// resumedAskedBook is S4's book: turn 1 asked about drafts 42 and 43 and was resumed by a free answer; turn 2,
// resuming it, revised draft 42 to rev 2 and asks again.
func resumedAskedBook() turn.Book {
	b := askedBook()
	b.Turns[0].Seq, b.Turns[0].State = 1, turn.Settled
	again := optionsAsked()
	again.Question.Options[0].Draft = &record.DraftRef{ID: 42, Rev: 2}
	b.Turns = append(b.Turns, turn.Turn{Seq: 2, ResumeOf: 1, Intent: b.Turns[0].Intent, ID: "q2", Trigger: turn.Automated, State: turn.Asked,
		Session: "s1", SentAt: t0.Add(-30 * time.Minute)})
	b.Records = append(b.Records, turn.Record{TurnSeq: 2, Session: "s1", At: t0, Hash: "h2", Record: again})
	return b
}

// TestASentDraftOfAnEarlierTurnEndsTheResumedQuestion (S4): draft 42, proposed by turn 1 and revised by turn 2,
// is sent after turn 2's question picked it, and turn 2 ends done on it.
func TestASentDraftOfAnEarlierTurnEndsTheResumedQuestion(t *testing.T) {
	ctx := context.Background()
	s, store, _, _, _ := newService(t)
	g := draftGate("42", task.ActorPerson, "", "")
	g.ApprovedRev = 2
	s.DraftGates, s.DraftVerdict = draftGates{42: g}, eventVerdict
	store.Put(resumedAskedBook())
	d := sentDraft(42, 1)
	d.Rev = 2
	if err := s.DraftSent(ctx, d); err != nil {
		t.Fatal(err)
	}
	got := bookOf(t, store, r7)
	last, _ := got.LastRecord(2)
	if o := last.Record.Outcome; got.Turns[1].State != turn.Done || o == nil || o.Draft == nil || *o.Draft != (record.DraftRef{ID: 42, Rev: 2}) ||
		o.URL != d.Evidence.Permalink || !o.Approved {
		t.Fatalf("turn 2 %s, record %+v outcome %+v: want done on draft 42 at rev 2", got.Turns[1].State, last.Record, o)
	}
}

// TestAFailedDraftOfAnEarlierTurnFreesTheResumedQuestion (S4): the person picked draft 42 on turn 2's question and
// the draft failed: turn 2 ends done on it, approved by nobody.
func TestAFailedDraftOfAnEarlierTurnFreesTheResumedQuestion(t *testing.T) {
	ctx := context.Background()
	s, store, _, _, _ := newService(t)
	b := resumedAskedBook()
	b.Answers = append(b.Answers, turn.Answer{TurnSeq: 2, OptionID: "approve", Route: turn.Approved, At: t0.Add(time.Minute)})
	store.Put(b)
	d := sentDraft(42, 1)
	d.State, d.Rev = draft.Failed, 2
	if err := s.DraftEnded(ctx, d); err != nil {
		t.Fatal(err)
	}
	got := bookOf(t, store, r7)
	last, _ := got.LastRecord(2)
	if o := last.Record.Outcome; got.Turns[1].State != turn.Done || o == nil || o.Draft == nil || o.Draft.ID != 42 || o.Approved {
		t.Fatalf("turn 2 %s, record %+v outcome %+v: want done on draft 42", got.Turns[1].State, last.Record, o)
	}
}
