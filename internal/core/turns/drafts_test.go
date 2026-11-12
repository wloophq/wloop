package turns_test

import (
	"context"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/core/gates"
	"github.com/wloophq/wloop/internal/core/turns"
	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/gate"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	"github.com/wloophq/wloop/internal/domain/record"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/turn"
)

type draftGates map[draft.ID]gate.Gate

func (g draftGates) GateOf(_ context.Context, d draft.Draft) (gate.Gate, bool, error) {
	got, ok := g[d.ID]
	return got, ok, nil
}

func optionsAsked() record.Record {
	return record.Record{State: record.Asked, Summary: "Approve, or comment?", Resume: "r",
		Outcome: &record.Outcome{Verdict: "APPROVE", NonBlocking: 2},
		Question: &record.Question{Kind: "choice", Text: "q", Options: []record.Option{
			{ID: "approve", Label: "Approve", Consequence: "c", Draft: &record.DraftRef{ID: 42, Rev: 1}},
			{ID: "comment", Label: "Comment", Consequence: "c", Draft: &record.DraftRef{ID: 43, Rev: 1}},
		}}}
}

func sentDraft(id draft.ID, seq int64) draft.Draft {
	return draft.Draft{ID: id, Unit: r7, State: draft.Sent, Rev: 5, Text: "the body",
		Origin:   draft.Origin{Kind: draft.OriginTurn, TurnSeq: seq, Session: "proposer"},
		Evidence: draft.Evidence{Ref: "c1", Permalink: "https://github.com/acme/api/pull/7#pullrequestreview-1"}}
}

func draftGate(id string, by task.Actor, rule, bySession string) gate.Gate {
	return gate.Gate{Kind: gate.KindDraft, Subject: id, Status: gate.Approved, By: by, Rule: rule, BySession: bySession,
		Session: "proposer", Sessions: []string{"proposer", "reviser"}, ApprovedRev: 1}
}

func askedBook() turn.Book {
	return turn.Book{Unit: r7, Turns: []turn.Turn{{Intent: turn.Intent(dintent.Review), ID: "q", Trigger: turn.Automated, State: turn.Asked, Session: "s1", SentAt: t0.Add(-time.Hour)}},
		Records: []turn.Record{{TurnSeq: 1, Session: "s1", At: t0.Add(-time.Minute), Hash: "h", Record: optionsAsked()}}}
}

func TestTurnDoneByApproval(t *testing.T) {
	ctx := context.Background()
	for _, c := range []struct {
		name     string
		g        gate.Gate
		approved bool
	}{
		{"a person approved it", draftGate("42", task.ActorPerson, "", ""), true},
		{"a session the user drives approved it", draftGate("42", task.ActorAgent, "", "driver"), true},
		{"a rule released it", draftGate("42", task.ActorWorkflow, "send", ""), false},
		{"a window released it", draftGate("42", task.ActorWorkflow, gates.RuleWindow, ""), false},
		{"the session that revised it", draftGate("42", task.ActorAgent, "", "reviser"), false},
		{"the session that proposed it", draftGate("42", task.ActorAgent, "", "proposer"), false},
		{"the gate of another draft", draftGate("43", task.ActorPerson, "", ""), false},
	} {
		s, store, _, _, rec := newService(t)
		s.DraftGates = draftGates{42: c.g}
		seq := store.Put(askedBook()).Turns[0].Seq
		if err := s.DraftSent(ctx, sentDraft(42, seq)); err != nil {
			t.Fatal(err)
		}
		got := bookOf(t, store, r7)
		last, _ := got.LastRecord(seq)
		o := last.Record.Outcome
		if got.Turns[0].State != turn.Done || last.Record.State != record.Done || o == nil || o.URL != sentDraft(42, seq).Evidence.Permalink ||
			o.Draft == nil || *o.Draft != (record.DraftRef{ID: 42, Rev: 1}) || o.Verdict != "APPROVE" || o.Approved != c.approved {
			t.Fatalf("%s: turn %s, record %+v outcome %+v", c.name, got.Turns[0].State, last.Record, o)
		}
		wantEndorsed := turn.NotEndorsed
		if c.approved {
			wantEndorsed = turn.EndorsedApproved
		}
		if e := got.Endorsed(seq); e != wantEndorsed {
			t.Fatalf("%s: endorsed %q", c.name, e)
		}
		if len(kinds(store.Events(), turns.KindTurnEnded)) != 1 || len(rec.launches) != 0 {
			t.Fatalf("%s: one turn end and no launch: %+v %+v", c.name, store.Events(), rec.launches)
		}
		if err := s.DraftSent(ctx, sentDraft(42, seq)); err != nil {
			t.Fatal(err)
		}
		if again := bookOf(t, store, r7); len(again.Records) != len(got.Records) {
			t.Fatalf("%s: sent again, recorded again: %+v", c.name, again.Records)
		}
	}
}

func TestADoneTurnsDraftFillsItsURL(t *testing.T) {
	ctx := context.Background()
	s, store, _, _, _ := newService(t)
	s.DraftGates = draftGates{42: draftGate("42", task.ActorWorkflow, "send", "")}
	done := record.Record{State: record.Done, Summary: "requested changes", Outcome: &record.Outcome{Verdict: "REQUEST_CHANGES", Draft: &record.DraftRef{ID: 42, Rev: 2}}}
	b := askedBook()
	b.Turns[0].State, b.Records[0].Record = turn.Done, done
	seq := store.Put(b).Turns[0].Seq
	if err := s.DraftSent(ctx, sentDraft(43, seq)); err != nil {
		t.Fatal(err)
	}
	if got := bookOf(t, store, r7); len(got.Records) != 1 {
		t.Fatalf("another draft of the turn fills nothing: %+v", got.Records)
	}
	if err := s.DraftSent(ctx, sentDraft(42, seq)); err != nil {
		t.Fatal(err)
	}
	got := bookOf(t, store, r7)
	last, _ := got.LastRecord(seq)
	if o := last.Record.Outcome; len(got.Records) != 2 || o.URL == "" || *o.Draft != (record.DraftRef{ID: 42, Rev: 2}) || o.Approved || got.Endorsed(seq) != turn.NotEndorsed {
		t.Fatalf("records %+v, outcome %+v", got.Records, o)
	}
}

func TestADraftOfNoQuestionEndsNothing(t *testing.T) {
	ctx := context.Background()
	s, store, _, _, _ := newService(t)
	s.DraftGates = draftGates{44: draftGate("44", task.ActorPerson, "", "")}
	seq := store.Put(askedBook()).Turns[0].Seq
	held := sentDraft(42, seq)
	held.State = draft.Held
	for _, d := range []draft.Draft{sentDraft(44, seq), sentDraft(42, 0), held} {
		if err := s.DraftSent(ctx, d); err != nil {
			t.Fatal(err)
		}
	}
	if got := bookOf(t, store, r7); got.Turns[0].State != turn.Asked || len(got.Records) != 1 {
		t.Fatalf("turn %s, records %+v", got.Turns[0].State, got.Records)
	}
}

func TestAnApprovingAnswerResumesNothing(t *testing.T) {
	ctx := context.Background()
	s, store, _, sess, rec := newService(t)
	store.Put(askedBook())
	req := turns.AnswerRequest{Unit: r7, OptionID: "approve", Gate: 9, Message: 3, Source: turn.Cockpit, Approves: true}
	res, err := s.Answer(ctx, req)
	if err != nil || res.Route != turn.Approved {
		t.Fatalf("%+v %v", res, err)
	}
	b := bookOf(t, store, r7)
	if len(rec.launches) != 0 || len(sess.Typed()) != 0 || b.Turns[0].State != turn.Asked || len(b.Answers) != 1 || b.Answers[0].Route != turn.Approved ||
		!b.QuestionAnswered(turn.Intent(dintent.Review)) {
		t.Fatalf("launches %v, typed %v, book %+v", rec.launches, sess.Typed(), b)
	}
	if again, err := s.Answer(ctx, req); err != nil || again.Route != turn.Approved || len(bookOf(t, store, r7).Answers) != 1 {
		t.Fatalf("the gate's consequence run again: %+v %v", again, err)
	}
}

func TestSourceWritesFalseDeniesManifestCommands(t *testing.T) {
	writes := [][]string{{"gh", "pr", "review"}, {"gh", "api", "*", "-X", "POST"}}
	base := turns.Policy{Deny: [][]string{{"git", "push"}}, Allow: [][]string{{"gh", "pr", "view"}}}
	denied := base.WithoutSourceWrites(dintent.Session{NoSourceWrites: true}, writes)
	if want := [][]string{{"git", "push"}, {"gh", "pr", "review"}, {"gh", "api", "*", "-X", "POST"}}; !reflect.DeepEqual(denied.Deny, want) {
		t.Fatalf("deny %v, want %v", denied.Deny, want)
	}
	if !reflect.DeepEqual(denied.Allow, base.Allow) || len(base.Deny) != 1 {
		t.Fatalf("the policy it starts from changed: %+v", base)
	}
	if kept := base.WithoutSourceWrites(dintent.Session{}, writes); !slices.EqualFunc(kept.Deny, base.Deny, slices.Equal) {
		t.Fatalf("source_writes left true denies nothing more: %v", kept.Deny)
	}
}

func TestNoGitHubWritesRemoved(t *testing.T) {
	if _, ok := reflect.TypeFor[turns.Policy]().FieldByName("NoGitHubWrites"); ok {
		t.Fatal("a session policy names no provider: the manifests' write_commands deny their writes")
	}
}
