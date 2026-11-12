package outbox

import (
	"testing"

	"github.com/wloophq/wloop/internal/domain/draft"
)

func BenchmarkProposeApproveSendAReview(b *testing.B) {
	for b.Loop() {
		b.StopTimer()
		e, _ := reviewEnv()
		b.StartTimer()
		d, err := e.svc.Propose(ctx, review("COMMENT"))
		if err != nil {
			b.Fatal(err)
		}
		if _, err := e.approve(d.ID, nil); err != nil {
			b.Fatal(err)
		}
		if _, _, err := e.svc.Send(ctx); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkListFiveHundredDrafts(b *testing.B) {
	e, _ := reviewEnv()
	for range 500 {
		e.propose(review("COMMENT"))
	}
	for b.Loop() {
		if ds, err := e.svc.List(ctx, Filter{States: []draft.State{draft.Held}}); err != nil || len(ds) != 500 {
			b.Fatal(len(ds), err)
		}
	}
}

func BenchmarkAgentAmend(b *testing.B) {
	for b.Loop() {
		b.StopTimer()
		e, _ := reviewEnv()
		d := e.propose(review("COMMENT"))
		cur := e.get(d.ID)
		b.StartTimer()
		if _, err := e.svc.AgentAmend(ctx, worker, d.ID, Amendment{Text: ptr("x"), Rev: cur.Rev}); err != nil {
			b.Fatal(err)
		}
	}
}
