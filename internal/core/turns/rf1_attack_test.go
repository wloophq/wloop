package turns_test

import (
	"context"
	"testing"

	"github.com/wloophq/wloop/internal/domain/record"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/turn"
)

// S3: the question pins #42 at rev 1 (verdict APPROVE in the asked record); the person edits the event on
// the card, which makes rev 2, and approves rev 2. Rev 1, the rev the record and its verdict stand for, was
// never approved, yet the done record says the user approved it.
func TestAttackApprovalOfAnotherRevEndorsesThePinnedVerdict(t *testing.T) {
	ctx := context.Background()
	s, store, _, _, _ := newService(t)
	g := draftGate("42", task.ActorPerson, "", "")
	g.ApprovedRev = 2
	s.DraftGates = draftGates{42: g}
	seq := store.Put(askedBook()).Turns[0].Seq
	d := sentDraft(42, seq)
	d.Rev = 2
	if err := s.DraftSent(ctx, d); err != nil {
		t.Fatal(err)
	}
	got := bookOf(t, store, r7)
	last, _ := got.LastRecord(seq)
	o := last.Record.Outcome
	if o.Approved && *o.Draft == (record.DraftRef{ID: 42, Rev: 1}) {
		t.Fatalf("outcome %+v (draft %+v) endorsed %q: verdict %s at rev 1 counts as approved by a rev-2 approval",
			o, *o.Draft, got.Endorsed(seq), o.Verdict)
	}
	_ = turn.NotEndorsed
}

// The agent's verdict is its own word: the record says APPROVE for a draft whose payload the person
// approved as it stands (a COMMENT, say). Nothing ties the verdict that history marks endorsed to what was
// posted.
func TestAttackAgentVerdictRidesOnTheUsersApproval(t *testing.T) {
	ctx := context.Background()
	s, store, _, _, _ := newService(t)
	s.DraftGates = draftGates{42: draftGate("42", task.ActorPerson, "", "")}
	seq := store.Put(askedBook()).Turns[0].Seq
	d := sentDraft(42, seq)
	d.Rev = 1
	d.Payload = []byte(`{"event":"COMMENT"}`)
	if err := s.DraftSent(ctx, d); err != nil {
		t.Fatal(err)
	}
	got := bookOf(t, store, r7)
	if v, ok := got.LastVerdict(turn.Intent("review")); ok && v.Verdict == "APPROVE" && v.Endorsed == turn.EndorsedApproved {
		t.Fatalf("last verdict %+v: an APPROVE the user never approved reads as endorsed", v)
	}
}
