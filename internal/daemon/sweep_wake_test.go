package daemon

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/wloophq/wloop/internal/core/change"
	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/domain/unit"
)

var wakeStart = time.Date(2026, 10, 21, 9, 0, 0, 0, time.UTC)

// stepClock is a clock that moves only when a test moves it: an After
// whose instant has come is ready at once, any other never fires.
type stepClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *stepClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *stepClock) After(d time.Duration) <-chan time.Time {
	ch := make(chan time.Time, 1)
	if d <= 0 {
		ch <- c.Now()
	}
	return ch
}

func (c *stepClock) set(t time.Time) {
	c.mu.Lock()
	c.now = t
	c.mu.Unlock()
}

// fakeSweeper counts its runs; during a run it calls during, and it
// reports due and seen as the last sweep's.
type fakeSweeper struct {
	runs   atomic.Int32
	during func()
	// result is the outcome of run n, counted from 1; nil succeeds.
	result func(n int32) (skipped string, err error)
	due    func() time.Time
	seen   port.Generation
}

func (f *fakeSweeper) Sweep(ctx context.Context, loop sync.Locker) (string, string, error) {
	loop.Lock()
	defer loop.Unlock()
	n := f.runs.Add(1)
	if f.during != nil {
		f.during()
	}
	if f.result != nil {
		skipped, err := f.result(n)
		return "ok", skipped, err
	}
	return "ok", "", nil
}

func (f *fakeSweeper) Due() time.Time {
	if f.due == nil {
		return time.Time{}
	}
	return f.due()
}

func (f *fakeSweeper) Seen() port.Generation { return f.seen }

type wakeRig struct {
	s     *Server
	log   *change.Log
	clock *stepClock
	sw    *fakeSweeper
	job   *job
}

func newWakeRig(t *testing.T, change func(*Deps)) *wakeRig {
	t.Helper()
	r := &wakeRig{log: newChangeLog(), clock: &stepClock{now: wakeStart}, sw: &fakeSweeper{}}
	d := testDeps(t)
	d.Changes, d.Sweep = r.log, r.sw
	if change != nil {
		change(&d)
	}
	r.s = New(d)
	stopOnCleanup(t, r.s)
	r.job = r.s.jobs["sweep"]
	r.job.clock = r.clock
	t.Cleanup(r.s.wakeSweep())
	return r
}

func newChangeLog() *change.Log { return change.NewLog(change.DefaultWindow, nil) }

func (r *wakeRig) start(t *testing.T) {
	t.Helper()
	r.job.once(context.Background(), t.Logf, func(string, string) {})
}

// due runs every sweep that is due now, as the job's loop would, and
// returns how many ran.
func (r *wakeRig) due(t *testing.T) int {
	t.Helper()
	n := 0
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		ok := r.job.wait(ctx)
		cancel()
		if !ok {
			return n
		}
		r.job.once(context.Background(), t.Logf, func(string, string) {})
		n++
		if n > 10 {
			t.Fatal("the sweep runs without end")
		}
	}
}

func (r *wakeRig) at(t *testing.T, d time.Duration) int {
	t.Helper()
	r.clock.set(wakeStart.Add(d))
	return r.due(t)
}

var u7 = r7.Unit()

func TestSweepAtMostOncePerChange(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		r := newWakeRig(t, nil)
		r.start(t)
		noting := map[int]bool{2: true, 4: true, 10: true, 12: true, 22: true, 30: true, 44: true, 58: true}
		last := time.Duration(0)
		for m := 2; m <= 60; m += 2 {
			r.clock.set(wakeStart.Add(time.Duration(m) * time.Minute))
			r.log.Note(port.TopicJobs)
			if noting[m] {
				r.log.Note(port.TopicPRs, u7)
				r.log.Note(port.TopicSourceEvents, u7)
				last = time.Duration(m) * time.Minute
			}
			want := 0
			if noting[m] {
				want = 1
			}
			if n := r.due(t); n != want {
				t.Fatalf("minute %d: %d sweeps, want %d", m, n, want)
			}
		}
		if n := r.sw.runs.Load(); n != 1+8 {
			t.Fatalf("%d sweeps in the hour, want the start plus 8", n)
		}
		if n := r.at(t, last+time.Hour-time.Second); n != 0 {
			t.Fatalf("%d sweeps before the watchdog", n)
		}
		if n := r.at(t, last+time.Hour); n != 1 {
			t.Fatalf("watchdog: %d sweeps, want 1", n)
		}
	})
}

func TestIdleHourOneSweep(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		r := newWakeRig(t, nil)
		r.start(t)
		r.sw.runs.Store(0)
		for m := 2; m <= 60; m += 2 {
			r.clock.set(wakeStart.Add(time.Duration(m) * time.Minute))
			r.log.Note(port.TopicJobs)
			r.log.Note(port.TopicMeta)
			r.due(t)
		}
		if n := r.sw.runs.Load(); n != 1 {
			t.Fatalf("an idle hour ran %d sweeps, want 1 (the watchdog)", n)
		}
	})
}

func TestWatchdogRunsHourly(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		r := newWakeRig(t, nil)
		r.start(t)
		for h := 1; h <= 3; h++ {
			if n := r.at(t, time.Duration(h)*time.Hour-time.Second); n != 0 {
				t.Fatalf("hour %d: %d sweeps before the watchdog", h, n)
			}
			if n := r.at(t, time.Duration(h)*time.Hour); n != 1 {
				t.Fatalf("hour %d: %d sweeps, want 1", h, n)
			}
		}
	})
}

func TestKickDuringRunCoalesces(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		r := newWakeRig(t, nil)
		first := true
		r.sw.during = func() {
			if first {
				first = false
				for range 3 {
					r.log.Note(port.TopicPRs, u7)
				}
			}
		}
		r.start(t)
		if n := r.at(t, sweepSettle); n != 1 {
			t.Fatalf("3 notes during a run started %d runs after it, want 1", n)
		}
	})
}

func TestOwnWritesDoNotWake(t *testing.T) {
	t.Parallel()
	t.Run("events and rounds", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			r := newWakeRig(t, nil)
			r.sw.during = func() {
				r.log.Note(port.TopicLoopEvents, u7)
				r.log.Note(port.TopicRounds)
				r.log.Note(port.TopicJobs)
			}
			r.start(t)
			if n := r.due(t); n != 0 {
				t.Fatalf("the sweep's own writes woke %d runs", n)
			}
		})
	})
	t.Run("a snapshot read that moves the projection", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			r := newWakeRig(t, nil)
			r.sw.during = func() {
				r.log.Note(port.TopicAgents)
				r.sw.seen = r.log.Gen()
			}
			r.start(t)
			if n := r.due(t); n != 0 {
				t.Fatalf("the plan's own snapshot read woke %d runs", n)
			}
		})
	})
	t.Run("an agent change after the snapshot read", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			r := newWakeRig(t, nil)
			first := true
			r.sw.during = func() {
				r.sw.seen = r.log.Gen()
				if first {
					first = false
					r.log.Note(port.TopicAgents)
				}
			}
			r.start(t)
			if n := r.at(t, sweepSettle); n != 1 {
				t.Fatalf("an agent change the plan did not read woke %d runs, want 1", n)
			}
		})
	})
}

func TestFailedIngestWakesNoSweep(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		var r *wakeRig
		r = newWakeRig(t, func(d *Deps) {
			d.Collector, d.Ingest = failingSlow{}, []IngestJob{{ID: unit.GitHub, Job: failingIngest()}}
		})
		r.start(t)
		in := r.s.jobs[ingestJobName(unit.GitHub)]
		in.once(context.Background(), t.Logf, func(string, string) {})
		if st := in.status(); st.LastErr == "" {
			t.Fatalf("ingestion on a closed store should fail: %+v", st)
		}
		if n := r.due(t); n != 0 {
			t.Fatalf("a failed ingestion woke %d sweeps", n)
		}
	})
}

func TestFirstRunAfterStartPlans(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		for _, notes := range []int{0, 3} {
			r := newWakeRig(t, nil)
			for range notes {
				r.log.Note(port.TopicJobs)
			}
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan struct{})
			go func() { r.job.loop(ctx, t.Logf, func(string, string) {}); close(done) }()
			stop := sync.OnceFunc(func() {
				cancel()
				<-done
			})
			t.Cleanup(stop)
			synctest.Wait()
			stop()
			if n := r.sw.runs.Load(); n != 1 {
				t.Fatalf("%d notes before start: %d runs, want 1", notes, n)
			}
		}
	})
}

// deadlineRig sweeps with a deadline due after the start until it passes,
// and checks that a sweep runs at due, not before, and once.
func deadlineRig(t *testing.T, due time.Duration) {
	t.Helper()
	r := newWakeRig(t, nil)
	r.sw.due = func() time.Time {
		if at := wakeStart.Add(due); r.clock.Now().Before(at) {
			return at
		}
		return time.Time{}
	}
	r.start(t)
	before := r.at(t, due-time.Second)
	if want := min(int(due/time.Hour), 1); before != want {
		t.Fatalf("%d sweeps a second before the deadline, want %d (the watchdog)", before, want)
	}
	if n := r.at(t, due); n != 1 {
		t.Fatalf("the deadline ran %d sweeps, want 1", n)
	}
	if n := r.at(t, due+59*time.Minute); n != 0 {
		t.Fatalf("a spent deadline ran %d more sweeps", n)
	}
}

func TestConflictWindowWakesSweep(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		deadlineRig(t, 5*time.Minute)
	})
}

func TestCapRolloverWakesSweep(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		deadlineRig(t, 15*time.Hour)
	})
}

func TestBlindBlockWakesAfterADay(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		deadlineRig(t, 24*time.Hour)
	})
}

func TestALaterDeadlineReplacesAnEarlierOne(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		r := newWakeRig(t, nil)
		r.start(t)
		r.job.at(wakeStart.Add(10 * time.Minute))
		r.job.at(wakeStart.Add(20 * time.Minute))
		if n := r.at(t, 10*time.Minute); n != 0 {
			t.Fatalf("a replaced deadline ran %d sweeps", n)
		}
		if n := r.at(t, 20*time.Minute); n != 1 {
			t.Fatalf("the deadline ran %d sweeps, want 1", n)
		}
		r.job.at(wakeStart.Add(30 * time.Minute))
		r.job.at(time.Time{})
		if n := r.at(t, 30*time.Minute); n != 0 {
			t.Fatalf("a canceled deadline ran %d sweeps", n)
		}
	})
}

func TestAChangeRightAfterARunSettlesFirst(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		r := newWakeRig(t, nil)
		r.start(t)
		r.log.Note(port.TopicPRs, u7)
		if n := r.at(t, sweepSettle-time.Second); n != 0 {
			t.Fatalf("a change %v after a run ran %d sweeps", sweepSettle-time.Second, n)
		}
		if n := r.at(t, sweepSettle); n != 1 {
			t.Fatalf("a settled change ran %d sweeps, want 1", n)
		}
		r.job.at(wakeStart.Add(sweepSettle + time.Second))
		if n := r.at(t, sweepSettle+time.Second); n != 1 {
			t.Fatalf("a deadline right after a run ran %d sweeps, want 1", n)
		}
		r.job.trigger()
		if n := r.due(t); n != 1 {
			t.Fatalf("a kick right after a run ran %d sweeps, want 1", n)
		}
	})
}

func TestFailedSweepRetriesWithBackoff(t *testing.T) {
	t.Parallel()
	t.Run("failed", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			r := newWakeRig(t, nil)
			r.sw.result = func(n int32) (string, error) {
				if n <= 2 {
					return "", errors.New("github: 502")
				}
				return "", nil
			}
			r.start(t)
			for _, step := range []struct {
				at   time.Duration
				runs int
			}{{2*time.Minute - time.Second, 0}, {2 * time.Minute, 1}, {6*time.Minute - time.Second, 0}, {6 * time.Minute, 1},
				{6*time.Minute + time.Hour - time.Second, 0}, {6*time.Minute + time.Hour, 1}} {
				if n := r.at(t, step.at); n != step.runs {
					t.Fatalf("at +%v: %d sweeps, want %d", step.at, n, step.runs)
				}
			}
		})
	})
	t.Run("skipped with a deadline", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			r := newWakeRig(t, nil)
			r.sw.due = func() time.Time {
				if r.sw.runs.Load() == 1 {
					return wakeStart.Add(time.Minute)
				}
				return time.Time{}
			}
			r.sw.result = func(n int32) (string, error) {
				if n <= 3 {
					return "Orca not reachable", nil
				}
				return "", nil
			}
			r.start(t)
			r.job.at(wakeStart.Add(time.Minute))
			for _, step := range []struct {
				at   time.Duration
				runs int
			}{{time.Minute, 1}, {5*time.Minute - time.Second, 0}, {5 * time.Minute, 1}, {13*time.Minute - time.Second, 0}, {13 * time.Minute, 1},
				{13*time.Minute + time.Hour - time.Second, 0}, {13*time.Minute + time.Hour, 1}} {
				if n := r.at(t, step.at); n != step.runs {
					t.Fatalf("at +%v: %d sweeps, want %d", step.at, n, step.runs)
				}
			}
		})
	})
}

// sleepClock separates the wall clock from the time timers count, which
// stops while the machine sleeps.
type sleepClock struct {
	mu     sync.Mutex
	wall   time.Time
	awake  time.Duration
	timers []sleepTimer
}

type sleepTimer struct {
	at time.Duration
	ch chan time.Time
}

func (c *sleepClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.wall
}

func (c *sleepClock) After(d time.Duration) <-chan time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	ch := make(chan time.Time, 1)
	if d <= 0 {
		ch <- c.wall
		return ch
	}
	c.timers = append(c.timers, sleepTimer{c.awake + d, ch})
	return ch
}

func (c *sleepClock) sleep(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.wall = c.wall.Add(d)
}

func (c *sleepClock) run(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.awake += d
	c.wall = c.wall.Add(d)
	for _, tm := range c.timers {
		if tm.at <= c.awake {
			select {
			case tm.ch <- c.wall:
			default:
			}
		}
	}
}

func TestADeadlineSurvivesAMachineSleep(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		r := newWakeRig(t, nil)
		clk := &sleepClock{wall: wakeStart}
		r.job.clock = clk
		r.start(t)
		r.job.at(wakeStart.Add(30 * time.Minute))
		ctx, cancel := context.WithCancel(context.Background())
		done, exited := make(chan bool, 1), make(chan struct{})
		go func() {
			defer close(exited)
			done <- r.job.wait(ctx)
		}()
		t.Cleanup(func() {
			cancel()
			<-exited
		})
		synctest.Wait()
		clk.sleep(13 * time.Hour)
		clk.run(waitSlice)
		select {
		case ok := <-done:
			if !ok || r.job.deadline() != (time.Time{}) {
				t.Fatalf("wait %v, deadline left %v", ok, r.job.deadline())
			}
		case <-time.After(5 * time.Second):
			t.Fatal("a deadline the machine slept through waited past one slice after the wake")
		}
	})
}
