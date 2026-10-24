package daemon

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/meter"
)

type runFunc func(ctx context.Context) (summary, skipped string, err error)

// waitSlice bounds one timer of a job's wait: Go's timers stop while the
// machine sleeps, so a wait wakes at least this often to read the wall clock.
const waitSlice = time.Minute

type jobClock interface {
	Now() time.Time
	After(d time.Duration) <-chan time.Time
}

type wallClock struct{}

func (wallClock) Now() time.Time                         { return time.Now() }
func (wallClock) After(d time.Duration) <-chan time.Time { return time.After(d) }

type job struct {
	name    string
	every   time.Duration // the period, or the watchdog's for a job that wakes on change
	timeout time.Duration
	run     runFunc
	kick    chan struct{}
	onEnd   func() // called after each finished run, holding no lock; nil is none
	clock   jobClock
	wake    <-chan struct{}
	// stale says a wake finds nothing new since the last run; the wake is
	// then dropped. nil runs every wake.
	stale func() bool
	// settle is how long after a run ends a change wake waits; deadlines and
	// kicks do not wait.
	settle time.Duration
	// retry is the first backoff after a failure, doubling up to every; zero
	// backs off from every.
	retry time.Duration
	reset chan struct{}
	// woken is a change wake waiting for settle; only wait touches it.
	woken bool

	mu       sync.Mutex
	st       api.JobStatus
	failures int
	ended    time.Time
	due      time.Time
}

func newJob(name string, every, timeout time.Duration, run runFunc) *job {
	return &job{name: name, every: every, timeout: timeout, run: run, kick: make(chan struct{}, 1), reset: make(chan struct{}, 1),
		clock: wallClock{}, st: api.JobStatus{Name: name, Every: every}}
}

// wakeOn starts a run on every receive from c, as trigger does, unless
// stale says nothing changed.
func (j *job) wakeOn(c <-chan struct{}) { j.wake = c }

// at schedules one run at t; a later call replaces an earlier one, and the
// zero time cancels it.
func (j *job) at(t time.Time) {
	j.mu.Lock()
	j.due = t.Round(0)
	j.mu.Unlock()
	select {
	case j.reset <- struct{}{}:
	default:
	}
}

func (j *job) status() api.JobStatus {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.st
}

func (j *job) trigger() {
	select {
	case j.kick <- struct{}{}:
	default:
	}
}

func (j *job) once(ctx context.Context, log func(string, ...any), warn func(name, text string)) {
	j.mu.Lock()
	if j.st.Running {
		j.mu.Unlock()
		return
	}
	j.st.Running = true
	j.st.LastRun = j.clock.Now()
	j.mu.Unlock()
	cctx, cancel := context.WithTimeout(ctx, j.timeout)
	mctx, scope := meter.Start(cctx, meter.Job(j.name))
	_, skipped, err := safeRun(mctx, j.run)
	scope.End()
	cancel()
	j.mu.Lock()
	j.st.Running = false
	j.ended = j.clock.Now().Round(0)
	if errors.Is(err, context.Canceled) && errors.Is(ctx.Err(), context.Canceled) {
		j.mu.Unlock()
		return
	}
	if err != nil {
		j.failures++
		j.st.LastErr = err.Error()
		j.mu.Unlock()
		log("%s: %v", j.name, err)
	} else {
		j.failures = 0
		j.st.LastErr = ""
		j.st.LastOK = j.clock.Now()
		j.mu.Unlock()
		if skipped != "" {
			log("%s: %s", j.name, skipped)
		}
		warn(j.name, skipped)
	}
	if j.onEnd != nil {
		j.onEnd()
	}
}

// phase runs f under a scope named name, nested in the scope of ctx.
func phase(ctx context.Context, name string, f func(context.Context)) {
	pctx, scope := meter.Start(ctx, name)
	defer scope.End()
	f(pctx)
}

func safeRun(ctx context.Context, run runFunc) (summary, skipped string, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic: %v", r)
		}
	}()
	return run(ctx)
}

func (j *job) backoff() time.Duration {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.failures == 0 {
		return j.every
	}
	if j.retry > 0 {
		return min(j.retry<<min(j.failures-1, 10), j.every)
	}
	return min(j.every*time.Duration(1<<min(j.failures, 5)), time.Hour)
}

// next is when the job runs with no kick and no wake: its deadline, or a
// backoff after its last run ended, whichever comes first.
func (j *job) next() (at time.Time, deadline bool) {
	backoff := j.backoff()
	j.mu.Lock()
	defer j.mu.Unlock()
	at = j.ended.Add(backoff)
	if !j.due.IsZero() && j.due.Before(at) {
		return j.due, true
	}
	return at, false
}

func (j *job) deadline() time.Time {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.due
}

func (j *job) settled() time.Time {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.ended.Add(j.settle)
}

// clearDue drops the deadline t once it fired, unless a later at replaced it.
func (j *job) clearDue(t time.Time) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.due.Equal(t) {
		j.due = time.Time{}
	}
}

func (j *job) loop(ctx context.Context, log func(string, ...any), warn func(name, text string)) {
	j.once(ctx, log, warn)
	for j.wait(ctx) {
		j.once(ctx, log, warn)
	}
}

// wait blocks until the next run is due; false once ctx ends.
func (j *job) wait(ctx context.Context) bool {
	for {
		now := j.clock.Now().Round(0)
		at, deadline := j.next()
		if j.woken {
			settled := j.settled()
			if !settled.After(now) {
				j.woken = false
				return true
			}
			if settled.Before(at) {
				at, deadline = settled, false
			}
		}
		left := max(at.Sub(now), 0)
		slice := min(left, waitSlice)
		select {
		case <-ctx.Done():
			return false
		case <-j.clock.After(slice):
			if slice < left {
				continue
			}
			if deadline {
				j.clearDue(at)
			}
			j.woken = false
			return true
		case <-j.kick:
			j.woken = false
			return true
		case <-j.wake:
			if j.stale == nil || !j.stale() {
				j.woken = true
			}
		case <-j.reset:
		}
	}
}

type warnings struct {
	mu sync.Mutex
	m  map[string]string
}

func (w *warnings) set(name, text string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if text == "" {
		delete(w.m, name)
		return
	}
	if w.m == nil {
		w.m = map[string]string{}
	}
	w.m[name] = text
}

func (s *Server) jobDone(name, warning string) { s.warnings.set(name, warning) }

func (s *Server) jobEnded() { s.note(port.TopicJobs) }

func (w *warnings) list() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make([]string, 0, len(w.m))
	for name, text := range w.m {
		out = append(out, name+": "+text)
	}
	slices.Sort(out)
	return out
}

func (s *Server) noteConfigStale() {
	if s.deps.ConfigWarning == nil {
		return
	}
	s.warnings.set("config", s.deps.ConfigWarning())
}
