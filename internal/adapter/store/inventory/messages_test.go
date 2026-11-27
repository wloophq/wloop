package inventory

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/core/messages/messagestest"
	"github.com/wloophq/wloop/internal/domain/message"
	"github.com/wloophq/wloop/internal/domain/record"
	"github.com/wloophq/wloop/internal/domain/unit"
)

func TestMessageStoreContract(t *testing.T) {
	messagestest.Contract(t, func(t *testing.T) message.Store { return NewMessages(open(t).db) })
}

// TestAnOpenMessageReadsAsItsOwnRead holds what a page's one read of the open messages answers equal to each
// message's own read.
func TestAnOpenMessageReadsAsItsOwnRead(t *testing.T) {
	s := NewMessages(open(t).db)
	ctx := context.Background()
	at := time.Date(2026, 3, 2, 9, 0, 0, 0, time.UTC)
	var ids []int64
	for i, w := range []string{"widget-ask", "gear-ask", "bolt-ask"} {
		m, err := s.Upsert(ctx, message.Message{TaskID: "t-" + w, Unit: unit.Unit{Provider: "slack", Key: "T0/C0/" + w}, Kind: message.Question,
			Text: "Which " + w + "?", Options: []record.Option{{ID: "a", Label: "first"}}, Recommend: "a", Confirm: i == 1,
			Origin: message.Origin{Kind: message.OriginWorkflow, Workflow: w}, CreatedAt: at})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, m.ID)
	}
	if err := s.Close(ctx, ids[2], "first", at.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	open, err := s.Open(ctx)
	if err != nil || len(open) != 2 {
		t.Fatalf("Open = %d messages, %v; want 2", len(open), err)
	}
	for _, m := range open {
		one, ok, err := s.Message(ctx, m.ID)
		if err != nil || !ok || !reflect.DeepEqual(one, m) {
			t.Errorf("message %d: Open = %+v, Message = %+v, %v, %v", m.ID, m, one, ok, err)
		}
	}
}
