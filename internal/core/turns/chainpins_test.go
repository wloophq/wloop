package turns_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/core/turns"
	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/gate"
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
	b := resumedAskedBook()
	b.Answers = append(b.Answers, turn.Answer{TurnSeq: 2, OptionID: "approve", Route: turn.Approved, At: t0.Add(time.Minute)})
	store.Put(b)
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

// TestAnUnpickedPinsSendKeepsTheQuestion: draft 42, which turn 2's open question points to and no answer picked,
// is sent anyway (a rule queued it before the pin, or a person retried it from failed). The question still
// decides the other option's draft, so turn 2 still asks.
func TestAnUnpickedPinsSendKeepsTheQuestion(t *testing.T) {
	ctx := context.Background()
	s, store, _, _, _ := newService(t)
	g := draftGate("42", task.ActorWorkflow, "send", "")
	g.ApprovedRev = 2
	s.DraftGates, s.DraftVerdict = draftGates{42: g}, eventVerdict
	store.Put(resumedAskedBook())
	d := sentDraft(42, 1)
	d.Rev = 2
	if err := s.DraftSent(ctx, d); err != nil {
		t.Fatal(err)
	}
	if got := bookOf(t, store, r7); got.Turns[1].State != turn.Asked || len(got.Records) != 2 {
		t.Fatalf("turn 2 %s, records %+v: an unpicked draft's send ended the question", got.Turns[1].State, got.Records)
	}
}

// TestAPickAfterItsDraftSentEndsTheTurn: the sender posts draft 42 between its approval and the answer that picked
// it; the answer's route then ends the turn on the sent draft.
func TestAPickAfterItsDraftSentEndsTheTurn(t *testing.T) {
	ctx := context.Background()
	s, store, _, _, _ := newService(t)
	g := draftGate("42", task.ActorPerson, "", "")
	g.ApprovedRev = 2
	d := sentDraft(42, 1)
	d.Rev = 2
	s.DraftGates, s.DraftVerdict = storedDrafts{draftGates{42: g}, map[draft.ID]draft.Draft{42: d}}, eventVerdict
	store.Put(resumedAskedBook())
	if err := s.DraftSent(ctx, d); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Answer(ctx, turns.AnswerRequest{Unit: r7, OptionID: "approve", Gate: 9, Message: 3, Source: turn.Cockpit, Approves: true}); err != nil {
		t.Fatal(err)
	}
	got := bookOf(t, store, r7)
	last, _ := got.LastRecord(2)
	if o := last.Record.Outcome; got.Turns[1].State != turn.Done || o == nil || o.URL != d.Evidence.Permalink || !o.Approved {
		t.Fatalf("turn 2 %s, outcome %+v: want done on sent draft 42", got.Turns[1].State, o)
	}
}

// TestAQuestionPinsOnlyADraftItsAnswerDecides: a question's option may point only to a draft held with its gate
// open; one a rule already released, or one sent, posts whatever the person picks, so turn_end refuses the record
// and names each such pin.
func TestAQuestionPinsOnlyADraftItsAnswerDecides(t *testing.T) {
	ctx := context.Background()
	open := gate.Gate{Kind: gate.KindDraft, Subject: "43", Status: gate.Open}
	for _, c := range []struct {
		name string
		d42  draft.State
		g42  gate.Status
		want []turns.UnheldPin
	}{
		{"both held and open", draft.Held, gate.Open, nil},
		{"one queued by a rule", draft.Queued, gate.Approved, []turns.UnheldPin{{Where: "question.options[0].draft", Draft: 42, State: "queued", Gate: "approved"}}},
		{"one held whose gate is superseded", draft.Held, gate.Superseded, []turns.UnheldPin{{Where: "question.options[0].draft", Draft: 42, State: "held", Gate: "superseded"}}},
	} {
		s, store, _, _, _ := newService(t)
		startedRound(t, s, "abc")
		seq := bookOf(t, store, r7).Turns[0].Seq
		d42, d43 := sentDraft(42, seq), sentDraft(43, seq)
		d42.State, d43.State = c.d42, draft.Held
		g42 := open
		g42.Subject, g42.Status = "42", c.g42
		s.DraftGates = storedDrafts{draftGates{42: g42, 43: open}, map[draft.ID]draft.Draft{42: d42, 43: d43}}
		_, err := s.RecordTurnEnd(ctx, turns.RecordRequest{Session: "term_1", Turn: "abc", Record: optionsAsked()})
		var got turns.UnheldPins
		errors.As(err, &got)
		if !slices.Equal(got, c.want) || (c.want == nil) != (err == nil) {
			t.Errorf("%s: %v, want %+v", c.name, err, c.want)
		}
	}
}
