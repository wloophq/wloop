package messagestest

import (
	"context"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/domain/message"
	"github.com/wloophq/wloop/internal/domain/record"
	"github.com/wloophq/wloop/internal/domain/unit"
)

// Contract checks a message store's keys, closing and reads.
func Contract(t *testing.T, newStore func(t *testing.T) message.Store) {
	t.Run("questions key by turn while open", func(t *testing.T) { questionsKeyByTurn(t, newStore(t)) })
	t.Run("ledger proposals key by proposal", func(t *testing.T) { ledgerKeysByProposal(t, newStore(t)) })
	t.Run("workflow proposals key by rule while open", func(t *testing.T) { workflowKeysWhileOpen(t, newStore(t)) })
	t.Run("an id updates its row", func(t *testing.T) { idUpdates(t, newStore(t)) })
}

var (
	u  = unit.Unit{Provider: unit.GitHub, Key: "acme/api#7"}
	at = time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
)

func questionsKeyByTurn(t *testing.T, s message.Store) {
	ctx := context.Background()
	q := message.Message{Unit: u, Kind: message.Question, Text: "Publish?", Confirm: true, Origin: message.Origin{Kind: message.OriginTurn, TurnSeq: 3, Session: "term_1"}, CreatedAt: at}
	first, err := s.Upsert(ctx, q)
	if err != nil || first.ID == 0 || !first.Open() || !first.CreatedAt.Equal(at) {
		t.Fatalf("insert %+v %v", first, err)
	}
	q.Text, q.TaskID = "Publish now?", "t-1"
	q.Options = []record.Option{{ID: "a", Label: "A", Consequence: "does a"}}
	again, err := s.Upsert(ctx, q)
	if err != nil || again.ID != first.ID || again.Text != "Publish now?" || again.TaskID != "t-1" || len(again.Options) != 1 || !again.CreatedAt.Equal(at) {
		t.Fatalf("re-ask: %+v %v", again, err)
	}
	other := q
	other.Origin.TurnSeq = 4
	if m, _ := s.Upsert(ctx, other); m.ID == first.ID {
		t.Fatal("another turn's question took the first one's row")
	}
	if err := s.Close(ctx, first.ID, "yes", at.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(ctx, first.ID, "", at.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	got, ok, err := s.Message(ctx, first.ID)
	if err != nil || !ok || got.Open() || got.Answer != "yes" || !got.ClosedAt.Equal(at.Add(time.Minute)) {
		t.Fatalf("closed %+v %v %v", got, ok, err)
	}
	if err := s.Close(ctx, first.ID, "no, wait", at.Add(3*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if got, _, _ := s.Message(ctx, first.ID); got.Answer != "no, wait" || !got.ClosedAt.Equal(at.Add(time.Minute)) {
		t.Fatalf("a newer answer did not reword the answered message: %+v", got)
	}
	if again, _ := s.Upsert(ctx, q); again.ID == first.ID || !again.Open() {
		t.Fatalf("a question asked after its answer reused the closed message: %+v", again)
	}
	if _, ok, _ := s.Message(ctx, 999); ok {
		t.Fatal("found message 999")
	}
	open, err := s.Open(ctx)
	if err != nil || len(open) != 2 {
		t.Fatalf("open %+v %v", open, err)
	}
	if open[0].ID > open[1].ID {
		t.Fatalf("open not oldest first: %+v", open)
	}
}

func ledgerKeysByProposal(t *testing.T, s message.Store) {
	ctx := context.Background()
	led := message.Message{Unit: u, Kind: message.Proposal, Text: "accept?", Origin: message.Origin{Kind: message.OriginLedger, Proposal: 9}, CreatedAt: at}
	l1, _ := s.Upsert(ctx, led)
	if err := s.Close(ctx, l1.ID, "accept", at); err != nil {
		t.Fatal(err)
	}
	if l2, _ := s.Upsert(ctx, led); l2.ID != l1.ID || l2.Open() {
		t.Fatalf("a decided ledger proposal came back: %+v", l2)
	}
}

func workflowKeysWhileOpen(t *testing.T, s message.Store) {
	ctx := context.Background()
	p := message.Message{Unit: u, Kind: message.Proposal, Text: "nudge?", Origin: message.Origin{Kind: message.OriginWorkflow, Workflow: "nudge"}, CreatedAt: at}
	a, _ := s.Upsert(ctx, p)
	if b, _ := s.Upsert(ctx, p); a.ID != b.ID {
		t.Fatal("an open workflow proposal was duplicated")
	}
	if err := s.Close(ctx, a.ID, "", at); err != nil {
		t.Fatal(err)
	}
	if c, _ := s.Upsert(ctx, p); c.ID == a.ID {
		t.Fatal("a closed workflow proposal was reopened")
	}
	post := message.Message{Unit: u, Kind: message.AgentPost, Text: "fyi", Origin: message.Origin{Kind: message.OriginAgent, TurnSeq: 1}, CreatedAt: at}
	x, _ := s.Upsert(ctx, post)
	if y, _ := s.Upsert(ctx, post); x.ID == y.ID {
		t.Fatal("two agent posts share a message")
	}
}

func idUpdates(t *testing.T, s message.Store) {
	ctx := context.Background()
	post := message.Message{Unit: u, Kind: message.AgentPost, Text: "fyi", Origin: message.Origin{Kind: message.OriginAgent, TurnSeq: 1}, CreatedAt: at}
	x, _ := s.Upsert(ctx, post)
	x.TaskID = "t-9"
	y, err := s.Upsert(ctx, x)
	if err != nil || y.ID != x.ID || y.TaskID != "t-9" {
		t.Fatalf("update by id %+v %v", y, err)
	}
	if open, _ := s.Open(ctx); len(open) != 1 {
		t.Fatalf("open %+v", open)
	}
}
