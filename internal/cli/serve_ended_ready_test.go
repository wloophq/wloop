package cli

import (
	"context"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/wloophq/wloop/internal/core/change"
	"github.com/wloophq/wloop/internal/core/messages"
	"github.com/wloophq/wloop/internal/core/messages/messagestest"
	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/core/turns"
	"github.com/wloophq/wloop/internal/core/turns/turnstest"
	"github.com/wloophq/wloop/internal/domain/message"
	"github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
)

// noBooks hold no turn on any unit, as a pull request's book once it ended.
type noBooks struct{}

func (noBooks) Books(_ context.Context, units ...unit.Unit) (map[unit.Unit]turn.Book, error) {
	out := map[unit.Unit]turn.Book{}
	for _, u := range units {
		out[u] = turn.Book{Unit: u}
	}
	return out, nil
}

func (noBooks) BookOf(context.Context, turns.Key) (turn.Book, bool, error) {
	return turn.Book{}, false, nil
}

// TestAnEndedPullRequestNotedBeforeReadyIsReconciled: a pull request that ends while the daemon still builds its
// outbox is reconciled once the outbox is up, its question closed.
func TestAnEndedPullRequestNotedBeforeReadyIsReconciled(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		store := messagestest.New()
		u := ghUnit(7)
		m, err := store.Upsert(ctx, message.Message{Unit: u, Kind: message.Question, Text: "Approve?", CreatedAt: testNow,
			Origin: message.Origin{Kind: message.OriginTurn, TurnSeq: 1}})
		if err != nil {
			t.Fatal(err)
		}
		ready := make(chan struct{})
		now := func() time.Time { return testNow }
		s := messagestest.NewGates(now, nil).Bind(&messages.Service{Store: store, Books: noBooks{}, Intents: turnstest.Intents(),
			Clock: clockFunc(now), Log: t.Logf, Ready: ready})
		log := change.NewLog(64, t.Logf)
		var wg sync.WaitGroup
		wg.Go(func() { reconcileEnded(ctx, s, log, t.Logf) })
		t.Cleanup(func() {
			cancel()
			wg.Wait()
		})
		synctest.Wait()
		log.Note(port.TopicPRs, u)
		synctest.Wait()
		close(ready)
		synctest.Wait()
		got, ok, err := store.Message(ctx, m.ID)
		if err != nil || !ok || got.Open() {
			t.Fatalf("message %+v %v %v, want closed once the outbox is up", got, ok, err)
		}
	})
}
