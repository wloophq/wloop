package outbox

import (
	"context"
	"errors"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/wloophq/wloop/internal/domain/unit"
)

func TestRecentBasesShareOneReadWhileItIsFresh(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		live := &liveBases{}
		live.Set(pull, "c1")
		clock := &fakeClock{now: t0}
		var reads sync.WaitGroup
		b := &RecentBases{Bases: live, Clock: clock, Fresh: 30 * time.Second, ReadTimeout: 10 * time.Second, Reads: &reads}
		ctx := context.Background()
		for range 3 {
			if got, err := b.Recent(ctx, pull.Provider, pull, "review"); got != "c1" || err != nil {
				t.Fatalf("Recent = %q, %v", got, err)
			}
		}
		if live.Calls() != 1 {
			t.Fatalf("%d reads for three callers within Fresh, want 1", live.Calls())
		}
		live.Set(pull, "c2")
		if got, _ := b.Live(ctx, pull.Provider, pull, "review"); got != "c2" || live.Calls() != 2 {
			t.Fatalf("Live = %q after %d reads, want a read now", got, live.Calls())
		}
		clock.now = t0.Add(30 * time.Second)
		if got, _ := b.Recent(ctx, pull.Provider, pull, "review"); got != "c2" || live.Calls() != 3 {
			t.Fatalf("Recent once Fresh passed = %q after %d reads, want a new read", got, live.Calls())
		}
		reads.Wait()
	})
}

// heldBases answer only once release closes.
type heldBases struct{ release chan struct{} }

func (h heldBases) Live(ctx context.Context, _ unit.ProviderID, _ unit.Unit, _ string) (string, error) {
	select {
	case <-h.release:
		return "c2", nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

func TestARecentReadOutlivesTheCallerThatStartedIt(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := heldBases{release: make(chan struct{})}
		var reads sync.WaitGroup
		b := &RecentBases{Bases: h, Clock: &fakeClock{now: t0}, Fresh: 30 * time.Second, ReadTimeout: 10 * time.Second, Reads: &reads}
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		if _, err := b.Recent(ctx, pull.Provider, pull, "review"); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("a caller past its deadline: %v", err)
		}
		cancel()
		close(h.release)
		synctest.Wait()
		if got, err := b.Recent(context.Background(), pull.Provider, pull, "review"); got != "c2" || err != nil {
			t.Fatalf("the next caller = %q, %v, want the read the first started", got, err)
		}
		reads.Wait()
	})
}
