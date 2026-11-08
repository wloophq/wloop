package daemon

import (
	"context"
	"errors"
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

// pluginWakeRig runs a plugin job, whose runs it records and whose next the test sets, in a synctest bubble.
type pluginWakeRig struct {
	s    *Server
	mu   sync.Mutex
	runs []time.Time
	next time.Time
	err  error
}

func newPluginWakeRig(t *testing.T) *pluginWakeRig {
	t.Helper()
	r := &pluginWakeRig{}
	r.s = newServerWith(t, func(d *Deps) {
		d.Runtimes = []PluginRuntime{{ID: "thread", Jobs: []PluginJob{{Name: "extract:thread", Every: 5 * time.Minute, Retry: time.Minute,
			Run: func(context.Context) (string, string, time.Time, error) {
				r.mu.Lock()
				defer r.mu.Unlock()
				r.runs = append(r.runs, time.Now())
				next := r.next
				r.next = time.Time{}
				return "", "", next, r.err
			}}}}}
	})
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Go(func() { r.s.jobs["extract:thread"].loop(ctx, r.s.logf, r.s.jobDone) })
	t.Cleanup(func() { cancel(); wg.Wait() })
	synctest.Wait()
	return r
}

func (r *pluginWakeRig) ran() []time.Time {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]time.Time(nil), r.runs...)
}

func TestAWakeRunsAPluginJobAtExactlyItsTime(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		r := newPluginWakeRig(t)
		start := time.Now()
		at := start.Add(90 * time.Second)
		if !r.s.WakeJob("extract:thread", at) {
			t.Fatal("no job extract:thread")
		}
		time.Sleep(2 * time.Minute)
		synctest.Wait()
		if got := r.ran(); len(got) != 2 || !got[1].Equal(at) {
			t.Fatalf("runs at %v, want the start and %v", got, at)
		}
	})
}

func TestTheEarlierOfTwoWakesSurvives(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		r := newPluginWakeRig(t)
		start := time.Now()
		r.s.WakeJob("extract:thread", start.Add(2*time.Minute))
		r.s.WakeJob("extract:thread", start.Add(time.Minute))
		r.s.WakeJob("extract:thread", start.Add(3*time.Minute))
		time.Sleep(4 * time.Minute)
		synctest.Wait()
		if got := r.ran(); len(got) != 2 || !got[1].Equal(start.Add(time.Minute)) {
			t.Fatalf("runs at %v, want the start and a minute later only", got)
		}
	})
}

func TestARunsNextRunsTheJobThen(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		r := newPluginWakeRig(t)
		start := time.Now()
		r.mu.Lock()
		r.next = start.Add(100 * time.Second)
		r.mu.Unlock()
		r.s.WakeJob("extract:thread", time.Time{})
		time.Sleep(3 * time.Minute)
		synctest.Wait()
		if got := r.ran(); len(got) != 3 || !got[1].Equal(start) || !got[2].Equal(start.Add(100*time.Second)) {
			t.Fatalf("runs at %v, want the start twice and %v", got, start.Add(100*time.Second))
		}
	})
}

func TestAFailedRunsNextWaitsForTheRetry(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		r := newPluginWakeRig(t)
		start := time.Now()
		r.mu.Lock()
		r.err = errors.New("the store is locked")
		r.next = start
		r.mu.Unlock()
		r.s.WakeJob("extract:thread", time.Time{})
		time.Sleep(90 * time.Second)
		synctest.Wait()
		if got := r.ran(); len(got) != 3 || !got[1].Equal(start) || !got[2].Equal(start.Add(time.Minute)) {
			t.Fatalf("runs at %v, want the start twice and the retry a minute later, not the failed run's next", got)
		}
	})
}

func TestAPluginJobWithNoWakeWaitsForItsInterval(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		r := newPluginWakeRig(t)
		start := time.Now()
		time.Sleep(5*time.Minute + time.Second)
		synctest.Wait()
		if got := r.ran(); len(got) != 2 || !got[1].Equal(start.Add(5*time.Minute)) {
			t.Fatalf("runs at %v, want the start and the backstop five minutes later", got)
		}
	})
}

func TestAWakeOfAnUnknownJobIsRefused(t *testing.T) {
	t.Parallel()
	s := newServer(t)
	if s.WakeJob("extract:nobody", time.Time{}) {
		t.Fatal("woke a job that does not exist")
	}
}
