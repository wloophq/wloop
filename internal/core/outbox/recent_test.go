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
		if got, _ := b.Recent(ctx, pull.Provider, pull, "review"); got != "c1" {
			t.Fatalf("Recent once Fresh passed = %q, want the last answer while a new read runs", got)
		}
		synctest.Wait()
		if got, _ := b.Recent(ctx, pull.Provider, pull, "review"); got != "c2" || live.Calls() != 3 {
			t.Fatalf("Recent after the new read = %q after %d reads, want its answer", got, live.Calls())
		}
		reads.Wait()
	})
}

// switchBases answer at once until hold closes the gate, then only once release closes.
type switchBases struct {
	mu      sync.Mutex
	live    string
	err     error
	hold    bool
	release chan struct{}
}

func (s *switchBases) Live(ctx context.Context, _ unit.ProviderID, _ unit.Unit, _ string) (string, error) {
	s.mu.Lock()
	hold, release := s.hold, s.release
	s.mu.Unlock()
	if hold {
		select {
		case <-release:
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.live, s.err
}

func (s *switchBases) set(f func(*switchBases)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f(s)
}

// TestARecentReadPastFreshNeverWaits answers a caller once Fresh passed with the last read's answer, failed or
// not, at once, while the next read is on its way.
func TestARecentReadPastFreshNeverWaits(t *testing.T) {
	for _, tc := range []struct {
		name string
		live string
		err  error
	}{{name: "answered", live: "c1"}, {name: "failed", err: errors.New("no answer")}} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				s := &switchBases{live: tc.live, err: tc.err, release: make(chan struct{})}
				clock := &fakeClock{now: t0}
				var reads sync.WaitGroup
				defer reads.Wait()
				b := &RecentBases{Bases: s, Clock: clock, Fresh: 30 * time.Second, ReadTimeout: 10 * time.Second, Reads: &reads}
				ctx := context.Background()
				if got, err := b.Recent(ctx, pull.Provider, pull, "review"); got != tc.live || (err == nil) != (tc.err == nil) {
					t.Fatalf("first Recent = %q, %v", got, err)
				}
				s.set(func(s *switchBases) { s.live, s.err, s.hold = "c2", nil, true })
				clock.now = t0.Add(35 * time.Second)
				start := time.Now()
				got, err := b.Recent(ctx, pull.Provider, pull, "review")
				if waited := time.Since(start); waited != 0 {
					t.Errorf("Recent past Fresh waited %v for the read on its way, want 0", waited)
				}
				if got != tc.live || (err == nil) != (tc.err == nil) {
					t.Errorf("Recent past Fresh = %q, %v, want the last answer", got, err)
				}
				close(s.release)
				synctest.Wait()
				if got, err := b.Recent(ctx, pull.Provider, pull, "review"); got != "c2" || err != nil {
					t.Errorf("Recent once the new read answered = %q, %v, want c2", got, err)
				}
			})
		})
	}
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

// TestARecentAnswerServesOnlyWhileTheNextReadCanLand serves the last answer while a new read runs only for
// ReadTimeout past Fresh, the most a steady stream of callers ever sees it age; a caller after that waits for
// the new read, as one of a key no read answered does.
func TestARecentAnswerServesOnlyWhileTheNextReadCanLand(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := &switchBases{live: "c1", release: make(chan struct{})}
		clock := &fakeClock{now: t0}
		var reads sync.WaitGroup
		defer reads.Wait()
		b := &RecentBases{Bases: s, Clock: clock, Fresh: 30 * time.Second, ReadTimeout: 10 * time.Second, Reads: &reads}
		ctx := context.Background()
		if got, _ := b.Recent(ctx, pull.Provider, pull, "review"); got != "c1" {
			t.Fatalf("first Recent = %q", got)
		}
		s.set(func(s *switchBases) { s.live, s.hold = "c2", true })
		clock.now = t0.Add(time.Hour)
		go func() {
			time.Sleep(time.Second)
			close(s.release)
		}()
		start := time.Now()
		got, err := b.Recent(ctx, pull.Provider, pull, "review")
		if got != "c2" || err != nil {
			t.Errorf("Recent an hour after the last answer = %q, %v, want the new read's", got, err)
		}
		if waited := time.Since(start); waited != time.Second {
			t.Errorf("Recent waited %v, want the new read's second", waited)
		}
	})
}
