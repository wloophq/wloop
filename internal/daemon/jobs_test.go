package daemon

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	ghsource "github.com/wloophq/wloop/internal/adapter/source/github"
	"github.com/wloophq/wloop/internal/core/port"
	coreusage "github.com/wloophq/wloop/internal/core/usage"
	dusage "github.com/wloophq/wloop/internal/domain/usage"
)

func TestJobRunsOnIntervalAndRecordsErrors(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		var runs atomic.Int32
		j := newJob("t", 10*time.Millisecond, time.Second, func(ctx context.Context) (string, string, error) {
			if runs.Add(1) == 2 {
				return "", "", errors.New("boom")
			}
			return "ok", "", nil
		})
		ctx, cancel := context.WithTimeout(context.Background(), 35*time.Millisecond)
		defer cancel()
		j.loop(ctx, func(string, ...any) {}, func(string, string) {})
		st := j.status()
		if runs.Load() != 3 || st.LastErr != "" {
			t.Fatalf("runs %d status %+v, want runs at 0, 10ms and 30ms after a failure's backoff", runs.Load(), st)
		}
		if st.LastRun.IsZero() || st.LastOK.IsZero() {
			t.Fatalf("status not recorded: %+v", st)
		}
	})
}

func TestJobBacksOffAfterFailure(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		var runs atomic.Int32
		j := newJob("t", 10*time.Millisecond, time.Second, func(context.Context) (string, string, error) {
			runs.Add(1)
			return "", "", errors.New("down")
		})
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()
		j.loop(ctx, func(string, ...any) {}, func(string, string) {})
		if n := runs.Load(); n != 3 {
			t.Fatalf("%d runs in 100ms at a 10ms interval, want 3: at 0, 20ms and 60ms", n)
		}
	})
}

func TestJobTriggerBeforeLoopIsNotLost(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		var runs atomic.Int32
		j := newJob("t", time.Hour, time.Second, func(context.Context) (string, string, error) { runs.Add(1); return "", "", nil })
		j.trigger()
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		j.loop(ctx, func(string, ...any) {}, func(string, string) {})
		if runs.Load() != 2 {
			t.Fatalf("runs: %d (want the initial run plus the queued trigger)", runs.Load())
		}
	})
}

func TestJobSkipIsAWarningNotAFailure(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		var warned string
		j := newJob("t", time.Hour, time.Second, func(context.Context) (string, string, error) { return "", "orca closed", nil })
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		j.loop(ctx, func(string, ...any) {}, func(name, text string) { warned = name + ": " + text })
		if st := j.status(); st.LastErr != "" || st.LastOK.IsZero() || warned != "t: orca closed" {
			t.Fatalf("%+v %q", st, warned)
		}
	})
}

func TestSkippedCollectorSweepIsRecordedAsAWarning(t *testing.T) {
	t.Parallel()
	sweep := func(context.Context) (ghsource.Report, error) {
		return ghsource.Report{Skipped: "rate limit at 100 of 5000, below 50%"}, nil
	}
	w := &warnings{}
	j := newJob("slow", time.Hour, time.Second, func(ctx context.Context) (string, string, error) { return ghsource.Outcome(sweep(ctx)) })
	j.once(context.Background(), func(string, ...any) {}, w.set)
	got := w.list()
	if len(got) != 1 || !strings.HasPrefix(got[0], "slow: rate limit at 100 of 5000") {
		t.Fatalf("warnings: %q", got)
	}
	if st := j.status(); st.LastErr != "" {
		t.Fatalf("a skipped sweep is not a failure: %+v", st)
	}
}

func TestCollectorReportJoinsTruncationAndWarnings(t *testing.T) {
	t.Parallel()
	sweep := func(context.Context) (ghsource.Report, error) {
		return ghsource.Report{Queries: 3, Fetched: 2, Truncated: []string{"q1"}, Warnings: []string{"team acme/x: 404"}}, nil
	}
	summary, skipped, err := func(ctx context.Context) (string, string, error) { return ghsource.Outcome(sweep(ctx)) }(context.Background())
	if err != nil || summary == "" {
		t.Fatalf("summary %q err %v", summary, err)
	}
	if !strings.Contains(skipped, "q1") || !strings.Contains(skipped, "team acme/x: 404") {
		t.Fatalf("skipped: %q", skipped)
	}
}

func TestWarningsClearAndSort(t *testing.T) {
	t.Parallel()
	w := &warnings{}
	w.set("slow", "b")
	w.set("fast", "a")
	w.set("sweep", "c")
	w.set("sweep", "")
	if got := w.list(); len(got) != 2 || got[0] != "fast: a" || got[1] != "slow: b" {
		t.Fatalf("%q", got)
	}
}

func TestJobPanicIsRecordedAndLoopContinues(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		var runs atomic.Int32
		j := newJob("t", 10*time.Millisecond, time.Second, func(context.Context) (string, string, error) {
			if runs.Add(1) == 1 {
				var m map[string]int
				m["x"] = 1
			}
			return "ok", "", nil
		})
		ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
		defer cancel()
		var logged []string
		j.loop(ctx, func(format string, args ...any) { logged = append(logged, fmt.Sprintf(format, args...)) }, func(string, string) {})
		if runs.Load() != 2 {
			t.Fatalf("%d runs, want the panic and one more after its backoff", runs.Load())
		}
		if len(logged) == 0 || !strings.Contains(logged[0], "panic") {
			t.Fatalf("panic not logged: %q", logged)
		}
	})
}

func TestJobPanicSetsLastErr(t *testing.T) {
	t.Parallel()
	j := newJob("t", time.Hour, time.Second, func(context.Context) (string, string, error) { panic("bad shape") })
	j.once(context.Background(), func(string, ...any) {}, func(string, string) {})
	if st := j.status(); st.Running || !strings.Contains(st.LastErr, "bad shape") {
		t.Fatalf("%+v", st)
	}
}

func TestJobRunGetsTheJobTimeout(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		var left time.Duration
		j := newJob("t", time.Hour, 3*time.Second, func(ctx context.Context) (string, string, error) {
			if dl, ok := ctx.Deadline(); ok {
				left = time.Until(dl)
			}
			return "", "", nil
		})
		j.once(context.Background(), func(string, ...any) {}, func(string, string) {})
		if left != 3*time.Second {
			t.Fatalf("deadline in %v, want 3s", left)
		}
	})
}

func TestJobSkipIsLogged(t *testing.T) {
	t.Parallel()
	j := newJob("t", time.Hour, time.Second, func(context.Context) (string, string, error) { return "", "orca closed", nil })
	var logged string
	j.once(context.Background(), func(format string, args ...any) { logged = fmt.Sprintf(format, args...) }, func(string, string) {})
	if logged != "t: orca closed" {
		t.Fatalf("logged %q", logged)
	}
}

func TestJobStatusCarriesItsInterval(t *testing.T) {
	t.Parallel()
	j := newJob("fast", 7*time.Minute, time.Second, func(context.Context) (string, string, error) { return "", "", nil })
	if got := j.status().Every; got != 7*time.Minute {
		t.Fatalf("Every = %s", got)
	}
}

type usageStore struct {
	pending []coreusage.Turn
	saved   map[int64]string
}

func (u *usageStore) Pending(context.Context, time.Time, int) ([]coreusage.Turn, error) {
	return u.pending, nil
}

func (u *usageStore) SaveUsage(_ context.Context, seq int64, _ []dusage.Model, source string, _ time.Time, _ ...port.Event) error {
	u.saved[seq] = source
	return nil
}

func (u *usageStore) Attempted(context.Context, int64) error { return nil }

func TestTheUsageJobRunsOnTheSlowTick(t *testing.T) {
	t.Parallel()
	d := testDeps(t)
	store := &usageStore{pending: []coreusage.Turn{{Seq: 3, Kind: "echo", AgentSession: "a1"}}, saved: map[int64]string{}}
	d.Usage = &coreusage.Service{Store: store, Clock: fixedClock(time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC))}
	s := New(d)
	j, ok := s.jobs["usage"]
	if !ok {
		t.Fatal("no usage job")
	}
	if got := j.status().Every; got != time.Duration(d.Cfg.Sweep.Slow) {
		t.Fatalf("every %v, want the slow tick %v", got, time.Duration(d.Cfg.Sweep.Slow))
	}
	j.once(context.Background(), t.Logf, func(string, string) {})
	if st := j.status(); st.LastErr != "" || st.LastOK.IsZero() {
		t.Fatalf("status %+v", st)
	}
	if store.saved[3] != coreusage.SourceUnavailable {
		t.Fatalf("saved %v", store.saved)
	}
	if _, ok := New(testDeps(t)).jobs["usage"]; ok {
		t.Fatal("a usage job without a usage service")
	}
}

func TestTheRetainJobRunsHourly(t *testing.T) {
	t.Parallel()
	d := testDeps(t)
	calls := 0
	d.Retain = func(context.Context) (string, error) {
		calls++
		return "retain: events 3", nil
	}
	j, ok := New(d).jobs["retain"]
	if !ok {
		t.Fatal("no retain job")
	}
	if j.every != time.Hour || j.timeout != 2*time.Minute {
		t.Fatalf("every %v timeout %v, want 1h and 2m", j.every, j.timeout)
	}
	j.once(context.Background(), t.Logf, func(string, string) {})
	if st := j.status(); calls != 1 || st.LastErr != "" || st.LastOK.IsZero() {
		t.Fatalf("%d calls, status %+v", calls, st)
	}
	if _, ok := New(testDeps(t)).jobs["retain"]; ok {
		t.Fatal("a retain job without Retain")
	}
}

func TestJobOnceStopsQuietlyOnShutdownButRecordsATimeout(t *testing.T) {
	t.Parallel()
	noop := func(string, ...any) {}
	noWarn := func(string, string) {}

	t.Run("shutdown cancel stamps nothing", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		j := newJob("t", time.Hour, time.Minute, func(ctx context.Context) (string, string, error) {
			cancel()
			return "partial", "", ctx.Err()
		})
		var logged int
		j.once(ctx, func(string, ...any) { logged++ }, noWarn)
		st := j.status()
		if st.Running || !st.LastOK.IsZero() || st.LastErr != "" || logged != 0 {
			t.Fatalf("status = %+v, logged %d; want nothing stamped", st, logged)
		}
	})

	t.Run("timeout records the error", func(t *testing.T) {
		j := newJob("t", time.Hour, 10*time.Millisecond, func(ctx context.Context) (string, string, error) {
			<-ctx.Done()
			return "", "", ctx.Err()
		})
		j.once(context.Background(), noop, noWarn)
		st := j.status()
		if st.LastErr == "" || !st.LastOK.IsZero() {
			t.Fatalf("status = %+v, want LastErr set and no LastOK", st)
		}
	})
}
