package brief

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/wloophq/wloop/internal/adapter/store/inventory"
	"github.com/wloophq/wloop/internal/core/turns"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/meter"
	"github.com/wloophq/wloop/internal/meter/metertest"
)

type fakeTimers struct {
	mu    sync.Mutex
	waits []time.Duration
	chans []chan time.Time
}

func (f *fakeTimers) After(d time.Duration) <-chan time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	c := make(chan time.Time, 1)
	f.waits, f.chans = append(f.waits, d), append(f.chans, c)
	return c
}

func (f *fakeTimers) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.waits)
}

func (f *fakeTimers) wait(i int) time.Duration {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.waits[i]
}

func (f *fakeTimers) fire(i int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.chans[i] <- time.Time{}
}

type countingStore struct {
	Store
	scans atomic.Int64
}

func (c *countingStore) BriefStates(ctx context.Context) ([]inventory.BriefState, error) {
	c.scans.Add(1)
	return c.Store.BriefStates(ctx)
}

type failingStore struct{ Store }

func (failingStore) BriefStates(context.Context) ([]inventory.BriefState, error) {
	return nil, fmt.Errorf("disk gone")
}

type failingStart struct {
	Store
	starts atomic.Int64
}

func (f *failingStart) StartBriefRun(context.Context, dinv.BriefRun) (int64, error) {
	f.starts.Add(1)
	return 0, fmt.Errorf("disk full")
}

type scheduled struct {
	*rig
	timers *fakeTimers
	store  *countingStore
}

func newScheduled(t *testing.T, n int) *scheduled {
	t.Helper()
	r := newRig(t, n)
	s := &scheduled{rig: r, timers: &fakeTimers{}, store: &countingStore{Store: r.st}}
	r.q.d.After, r.q.d.Store = s.timers.After, s.store
	return s
}

func (s *scheduled) start(t *testing.T, ctx context.Context) {
	t.Helper()
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		s.q.Start(ctx)
		close(done)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})
}

// sleeps waits for the scheduler to park and counts the sleeps it asked for.
func (s *scheduled) sleeps() int {
	synctest.Wait()
	return s.timers.count()
}

func (s *scheduled) wantSleeps(t *testing.T, n int) {
	t.Helper()
	if got := s.sleeps(); got != n {
		t.Fatalf("%d sleeps, want %d", got, n)
	}
}

func due(q *Queue) bool {
	select {
	case <-q.Due():
		return true
	default:
		return false
	}
}

func TestNextPicksTheEarliestDeadline(t *testing.T) {
	r := newRig(t, 0)
	_, v := r.q.version(refN(1))
	state := func(n int, rec dinv.BriefRecord) inventory.BriefState {
		p := dinv.PR{Ref: refN(n), State: "OPEN", HeadOid: "h1", ChangedFiles: 3, Reasons: []string{"repo"}}
		rec.PR = p.Ref
		if rec.AttemptOf == "keep" {
			rec.AttemptOf = attemptOf(p, v)
		}
		return inventory.BriefState{PR: p, Rec: rec, Has: true}
	}
	wanted := func(n int, retry time.Duration) inventory.BriefState {
		return state(n, dinv.BriefRecord{AttemptOf: "keep", WantedAt: t0, RetryAt: t0.Add(retry)})
	}
	failed := func(n int, ago time.Duration, failures int) inventory.BriefState {
		return state(n, dinv.BriefRecord{AttemptOf: "keep", Failures: failures, Error: "boom", AttemptedAt: t0.Add(-ago)})
	}
	gaveUp := wanted(9, time.Minute)
	gaveUp.Rec.GaveUpAt = t0
	open := func() { r.q.breaker = breaker{open: true, since: t0, next: t0.Add(BreakerCool)} }
	for _, tc := range []struct {
		name   string
		states []inventory.BriefState
		setup  func()
		want   time.Duration
		ok     bool
	}{
		{name: "none"},
		{name: "a wanted retry", states: []inventory.BriefState{wanted(1, 4*time.Minute)}, want: 4 * time.Minute, ok: true},
		{name: "the earliest of two wanted retries", states: []inventory.BriefState{wanted(1, 4*time.Minute), wanted(2, time.Minute)}, want: time.Minute, ok: true},
		{name: "a given-up want waits for nothing", states: []inventory.BriefState{gaveUp}},
		{name: "a backoff end", states: []inventory.BriefState{failed(1, 10*time.Minute, 1)}, want: 50 * time.Minute, ok: true},
		{name: "a backoff already over is the job's", states: []inventory.BriefState{failed(1, 2*time.Hour, 1)}},
		{name: "the retry before the backoff", states: []inventory.BriefState{failed(1, 10*time.Minute, 1), wanted(2, time.Minute)}, want: time.Minute, ok: true},
		{name: "the breaker's trial", states: []inventory.BriefState{wanted(1, time.Minute)}, setup: open, want: BreakerCool, ok: true},
		{name: "a trial under way", setup: func() { open(); r.q.breaker.trial = true }},
		{name: "an open breaker past its cool with no candidate", setup: func() { open(); r.q.breaker.next = t0.Add(-time.Minute) }},
		{name: "an open breaker past its cool keeps the wanted retry", states: []inventory.BriefState{wanted(1, time.Minute)},
			setup: func() { open(); r.q.breaker.next = t0.Add(-time.Minute) }, want: time.Minute, ok: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r.q.breaker = breaker{}
			if tc.setup != nil {
				tc.setup()
			}
			got, ok := r.q.Next(tc.states, t0)
			if ok != tc.ok || ok && !got.Equal(t0.Add(tc.want)) {
				t.Fatalf("Next = %v, %v; want %v, %v", got, ok, t0.Add(tc.want), tc.ok)
			}
		})
	}
}

func TestStartSleepsUntilTheEarliestDeadline(t *testing.T) {
	t.Run("an idle hour scans at most seven times", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			s := newScheduled(t, 3)
			ctx, sc := metertest.Start(t, context.Background())
			s.start(t, ctx)
			for i := range 7 {
				s.wantSleeps(t, i+1)
				if got := s.timers.wait(i); got != Resync {
					t.Fatalf("sleep %d is %v, want %v", i, got, Resync)
				}
				if i < 6 {
					s.now = s.now.Add(Resync)
					s.timers.fire(i)
				}
			}
			if n := s.store.scans.Load(); n != 7 {
				t.Fatalf("%d scans in an hour, want 7", n)
			}
			metertest.Within(t, sc, metertest.Budget{
				{Res: meter.SQL, Op: "query"}: 14,
				{Res: meter.SQL, Op: "exec"}:  0,
				{Res: meter.Tx, Op: "write"}:  0,
			})
		})
	})
	t.Run("a long backoff sleeps Resync", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			s := newScheduled(t, 1)
			ctx := context.Background()
			run := dinv.BriefRun{PR: refN(1), Trigger: TriggerBackground, StartedAt: t0.Add(-time.Minute), EndedAt: t0, Outcome: OutcomeFailed, Error: "boom"}
			_, v := s.q.version(refN(1))
			p, _, _ := s.st.PR(ctx, refN(1))
			run.AttemptOf, run.Version = attemptOf(p, v), v
			id, err := s.st.StartBriefRun(ctx, run)
			if err != nil {
				t.Fatal(err)
			}
			run.ID = id
			if err := s.st.EndBriefRun(ctx, run, dinv.BriefRecord{AttemptOf: run.AttemptOf, Failures: 5}); err != nil {
				t.Fatal(err)
			}
			s.q.d.Retry = 2 * time.Hour
			s.start(t, ctx)
			s.wantSleeps(t, 1)
			if got := s.timers.wait(0); got != Resync {
				t.Fatalf("sleeps %v, want %v", got, Resync)
			}
		})
	})
	t.Run("an open breaker with no candidate sleeps Resync and reads once", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			s := newScheduled(t, 0)
			s.q.breaker = breaker{open: true, since: t0, next: t0.Add(-time.Minute)}
			s.start(t, context.Background())
			s.wantSleeps(t, 1)
			if got, n := s.timers.wait(0), s.store.scans.Load(); got != Resync || n != 1 {
				t.Fatalf("sleeps %v after %d reads, want %v after 1", got, n, Resync)
			}
		})
	})
	t.Run("a scan that cannot read sleeps scanRetry", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			s := newScheduled(t, 0)
			s.q.d.Store = failingStore{s.store}
			s.start(t, context.Background())
			s.wantSleeps(t, 1)
			if got := s.timers.wait(0); got != scanRetry {
				t.Fatalf("sleeps %v, want %v", got, scanRetry)
			}
		})
	})
	t.Run("a wanted retry sleeps until its time", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			s := newScheduled(t, 1)
			s.run.fail["acme/api#1"] = fmt.Errorf("%w: kind %q", ErrInvalid, "epic")
			s.wantAndSettle(t, refN(1))
			s.start(t, context.Background())
			s.wantSleeps(t, 1)
			if got := s.timers.wait(0); got != WantedRetry[0] {
				t.Fatalf("sleeps %v, want %v", got, WantedRetry[0])
			}
		})
	})
}

func TestStartRetriesAWantedBriefNoOneWaitsOn(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := newScheduled(t, 1)
		s.run.fail["acme/api#1"] = fmt.Errorf("%w: kind %q", ErrInvalid, "epic")
		s.wantAndSettle(t, refN(1))
		delete(s.run.fail, "acme/api#1")
		s.start(t, context.Background())
		s.wantSleeps(t, 1)
		if n := s.store.scans.Load(); n != 1 || s.run.calls != 1 {
			t.Fatalf("%d scans and %d runs before the retry is due", n, s.run.calls)
		}
		s.now = s.now.Add(WantedRetry[0])
		s.timers.fire(0)
		synctest.Wait()
		if s.record(t, refN(1)).Run == 0 {
			t.Fatal("the retry recorded no run")
		}
		if s.run.calls != 2 {
			t.Fatalf("%d runs, want the failure and the retry", s.run.calls)
		}
	})
}

func TestTouchedCoalesces(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := newScheduled(t, 2)
		s.q.Touched(nil)
		if len(s.q.wake) != 0 {
			t.Fatal("no refs woke the scheduler")
		}
		for range 50 {
			s.q.Touched([]pr.Ref{{Owner: "acme", Repo: "api", Number: 1}})
		}
		if len(s.q.wake) != 1 {
			t.Fatalf("%d wakes pending, want 1", len(s.q.wake))
		}
		s.start(t, context.Background())
		s.wantSleeps(t, 1)
		if n := s.store.scans.Load(); n != 1 {
			t.Fatalf("%d scans for 50 touches, want 1", n)
		}
	})
}

func TestTheTrialRunsAtTheBreakersNext(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := newScheduled(t, 2)
		s.run.fail["acme/api#1"] = fmt.Errorf("claude: %w", turns.ErrNoAgent)
		s.q.Run(context.Background())
		delete(s.run.fail, "acme/api#1")
		before := s.store.scans.Load()
		s.start(t, context.Background())
		s.wantSleeps(t, 1)
		if got := s.timers.wait(0); got != Resync {
			t.Fatalf("sleeps %v, want %v", got, Resync)
		}
		if n := s.store.scans.Load() - before; n != 0 || s.run.calls != 1 {
			t.Fatalf("%d scans and %d runs while the breaker cools", n, s.run.calls)
		}
		s.now = s.now.Add(Resync)
		s.timers.fire(0)
		s.wantSleeps(t, 2)
		if got := s.timers.wait(1); got != BreakerCool-Resync {
			t.Fatalf("sleeps %v, want %v", got, BreakerCool-Resync)
		}
		s.now = s.now.Add(BreakerCool - Resync)
		s.timers.fire(1)
		synctest.Wait()
		if s.q.isOpen() {
			t.Fatal("the trial left the breaker open")
		}
		if s.run.calls != 2 {
			t.Fatalf("%d runs, want the failure and one trial", s.run.calls)
		}
	})
}

func TestScanTellsTheJobAboutADuePullRequest(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := newScheduled(t, 1)
		s.start(t, context.Background())
		synctest.Wait()
		if !due(s.q) {
			t.Fatal("no Due for a followed pull request without a brief")
		}
	})
}

func TestRunWithNothingDueReadsOnceAndWritesNothing(t *testing.T) {
	r := newRig(t, 3)
	if _, err := r.q.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx, sc := metertest.Start(t, context.Background())
	summary, err := r.q.Run(ctx)
	if err != nil || summary != "0 briefed, 0 failed" {
		t.Fatalf("%q %v", summary, err)
	}
	metertest.Within(t, sc, metertest.Budget{
		{Res: meter.SQL}:             2,
		{Res: meter.SQL, Op: "exec"}: 0,
		{Res: meter.Tx, Op: "write"}: 0,
	})
}

func TestAStoreThatCannotWritePausesTheScheduler(t *testing.T) {
	ctx := context.Background()
	run := func(t *testing.T, s *scheduled) *failingStart {
		f := &failingStart{Store: s.store}
		s.q.d.Store = f
		s.start(t, ctx)
		// The failed run wakes the loop once or twice (its release, then a
		// trial's end), so the pause is one sleep or two.
		sleeps := s.sleeps()
		if sleeps < 2 {
			t.Fatalf("%d sleeps, want the first and the pause", sleeps)
		}
		for i := 1; i < sleeps; i++ {
			if got := s.timers.wait(i); got != scanRetry {
				t.Fatalf("sleep %d is %v after the failed write, want %v", i, got, scanRetry)
			}
		}
		if n, w := s.store.scans.Load(), f.starts.Load(); n != 1 || w != 1 {
			t.Fatalf("%d reads and %d writes before the pause ends, want 1 each", n, w)
		}
		s.now = s.now.Add(scanRetry)
		s.timers.fire(sleeps - 1)
		synctest.Wait()
		if n, w := s.store.scans.Load(), f.starts.Load(); n != 2 || w != 2 {
			t.Fatalf("%d reads and %d writes after one pause, want 2 each", n, w)
		}
		return f
	}
	t.Run("a wanted retry", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			s := newScheduled(t, 1)
			if err := s.st.WantBrief(ctx, refN(1), t0); err != nil {
				t.Fatal(err)
			}
			run(t, s)
		})
	})
	t.Run("the breaker's trial", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			s := newScheduled(t, 1)
			s.q.breaker = breaker{open: true, since: t0, next: t0.Add(-time.Minute)}
			run(t, s)
			if !s.q.isOpen() {
				t.Fatal("a trial that never ran closed the breaker")
			}
		})
	})
}

func TestTheEndOfATrialWakesTheLoop(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := newScheduled(t, 0)
		s.start(t, context.Background())
		s.wantSleeps(t, 1)
		s.q.mu.Lock()
		s.q.breaker.trial = true
		s.q.mu.Unlock()
		s.q.endTrial()
		s.wantSleeps(t, 2)
	})
}

func TestUnwantWakesTheSchedulerForTheBackgroundBrief(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := newScheduled(t, 1)
		ctx := context.Background()
		if err := s.st.WantBrief(ctx, refN(1), t0); err != nil {
			t.Fatal(err)
		}
		s.src.fail["acme/api#1"] = fmt.Errorf("claude: %w", turns.ErrUnavailable)
		s.start(t, ctx)
		s.wantSleeps(t, 2)
		if s.record(t, refN(1)).Error == "" || s.q.inFlight(refN(1)) {
			t.Fatal("the wanted run did not end in a failure")
		}
		if due(s.q) {
			t.Fatal("a wanted pull request was due a background brief")
		}
		if err := s.q.unwantPR(ctx, refN(1)); err != nil {
			t.Fatal(err)
		}
		s.wantSleeps(t, 3)
		if !due(s.q) {
			t.Fatal("dropping the want did not tell the job about the due pull request")
		}
	})
}
