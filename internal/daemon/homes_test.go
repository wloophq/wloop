package daemon

import (
	"context"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/wloophq/wloop/internal/api"
)

type homeProbe struct {
	calls   atomic.Int32
	release chan struct{}
}

func (p *homeProbe) check(context.Context) ([]api.HomeFinding, error) {
	p.calls.Add(1)
	if p.release != nil {
		<-p.release
	}
	return nil, nil
}

func wantCalls(t *testing.T, p *homeProbe, want int32, what string) {
	t.Helper()
	synctest.Wait()
	if n := p.calls.Load(); n != want {
		t.Fatalf("%s ran %d home checks, want %d", what, n, want)
	}
}

func TestFastCycleRunsNoHomeCheck(t *testing.T) {
	t.Parallel()
	d := testDeps(t)
	p := &homeProbe{}
	d.Homes = p.check
	d.Collector = failingSlow{}
	s := New(d)
	stopOnCleanup(t, s)
	for range 10 {
		s.jobs["fast"].once(context.Background(), t.Logf, func(string, string) {})
	}
	if n := p.calls.Load(); n != 0 {
		t.Fatalf("10 fast cycles ran %d home checks", n)
	}
}

func TestSessionStartNewHomeRechecks(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		s, _ := agentServer(t)
		p := &homeProbe{}
		s.deps.Homes = p.check
		start := func(home string) {
			t.Helper()
			body := `{"agent_session":"sess-1","cwd":"/wt/rev","home":"` + home + `"}`
			if rec := agentCall(t, s.Handler(), "POST", api.AgentSessionsStart, "term_1", body); rec.Code != http.StatusNoContent {
				t.Fatalf("start: %d %s", rec.Code, rec.Body)
			}
		}
		start("/h")
		wantCalls(t, p, 1, "a new home")
		start("/h")
		start("")
		wantCalls(t, p, 1, "a covered home or none")
	})
}

func TestSessionStartBurstOfNewHomesSharesOneRecheck(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		s, _ := agentServer(t)
		p := &homeProbe{release: make(chan struct{})}
		s.deps.Homes = p.check
		s.noteSessionHome("/h0")
		wantCalls(t, p, 1, "a new home")
		for _, h := range []string{"/h1", "/h2", "/h3", "/h4"} {
			s.noteSessionHome(h)
		}
		wantCalls(t, p, 1, "a burst during a check")
		close(p.release)
		wantCalls(t, p, 2, "a burst of 4 new homes")
	})
}

// A stale read races the background check it started: each round's reads
// must share one check however they interleave with it.
func TestStaleHomesRecheckOnRead(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		d := testDeps(t)
		var mu sync.Mutex
		now := time.Now()
		d.Now = func() time.Time { mu.Lock(); defer mu.Unlock(); return now }
		p := &homeProbe{}
		d.Homes = p.check
		s := New(d)
		stopOnCleanup(t, s)
		s.refreshHomes(context.Background())
		s.homeWarnings()
		wantCalls(t, p, 1, "a read of a fresh result")

		const rounds = 200
		for round := range int32(rounds) {
			mu.Lock()
			now = now.Add(HomesMaxAge + time.Minute)
			mu.Unlock()
			var wg sync.WaitGroup
			for range 5 {
				wg.Go(func() { s.homeWarnings() })
			}
			wg.Wait()
			wantCalls(t, p, round+2, "5 concurrent stale reads")
		}
	})
}
