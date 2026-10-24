package headless_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/core/headless"
	"github.com/wloophq/wloop/internal/core/turns"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	"github.com/wloophq/wloop/internal/domain/turn"
)

func (r *rig) spentToday(t *testing.T) float64 {
	t.Helper()
	got, err := r.spent(context.Background(), r.clock.Now())
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestFailedCallWithoutCostChargesTheCap(t *testing.T) {
	r := newRig(t, scripted{err: errors.New("killed")})
	if _, err := r.svc.Run(context.Background(), headless.Request{Unit: thread}); err == nil {
		t.Fatal("a failed call succeeded")
	}
	if got := r.spentToday(t); got != r.svc.Caps.PerCallUSD {
		t.Fatalf("spent %v, want the per-call cap %v", got, r.svc.Caps.PerCallUSD)
	}
}

func TestInterruptedTurnChargesTheCap(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	if _, err := r.turns.BeginHeadless(ctx, turns.HeadlessStart{Unit: thread, Intent: turn.Intent(dintent.Act), Turn: "cut", Trigger: turn.ByHand}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.newTurns().EndInterruptedHeadless(ctx, 0.5); err != nil {
		t.Fatal(err)
	}
	if got := r.spentToday(t); got != 0.5 {
		t.Fatalf("spent %v, want 0.5", got)
	}
}

// blocking answers only when its call's context ends, with no cost, as a killed claude -p does.
type blocking struct{ called chan struct{} }

func (b blocking) Ask(ctx context.Context, _ turns.Ask) (turns.Answer, error) {
	close(b.called)
	<-ctx.Done()
	return turns.Answer{}, ctx.Err()
}

func TestStartedCallEndsWithTheDaemon(t *testing.T) {
	for _, c := range []struct {
		name    string
		timeout time.Duration
		stop    bool
	}{{"stop", time.Hour, true}, {"timeout", 50 * time.Millisecond, false}} {
		t.Run(c.name, func(t *testing.T) {
			r := newRig(t)
			life, stop := context.WithCancel(context.Background())
			defer stop()
			ended := make(chan struct{})
			ask := blocking{called: make(chan struct{})}
			r.svc.Asker, r.svc.Context, r.svc.Timeout = ask, life, c.timeout
			r.svc.Go = func(run func()) { go func() { run(); close(ended) }() }
			req, cancelReq := context.WithCancel(context.Background())
			if _, err := r.svc.Start(req, headless.Request{Unit: thread}); err != nil {
				t.Fatal(err)
			}
			cancelReq()
			<-ask.called
			if c.stop {
				stop()
			}
			select {
			case <-ended:
			case <-time.After(5 * time.Second):
				t.Fatal("the call outlived the daemon")
			}
			if b := r.book(t, thread); b.Turns[0].State != turn.Failed {
				t.Fatalf("turn %+v", b.Turns[0])
			}
			if got := r.spentToday(t); got != r.svc.Caps.PerCallUSD {
				t.Fatalf("spent %v, want the per-call cap", got)
			}
		})
	}
}
