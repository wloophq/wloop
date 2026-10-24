package daemon

import (
	"context"
	"net/http"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	ghsource "github.com/wloophq/wloop/internal/adapter/source/github"
	"github.com/wloophq/wloop/internal/adapter/store/inventory"
	"github.com/wloophq/wloop/internal/adapter/store/sqlite/sqlitetest"
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

func waitCalls(t *testing.T, p *homeProbe, want int32) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for p.calls.Load() != want {
		if time.Now().After(deadline) {
			t.Fatalf("homes checks: %d, want %d", p.calls.Load(), want)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestFastCycleRunsNoHomeCheck(t *testing.T) {
	d := testDeps(t)
	closedDB, err := sqlitetest.Open(filepath.Join(d.StateDir, "closed.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	closed := inventory.New(closedDB)
	closedDB.Close()
	p := &homeProbe{}
	d.Homes = p.check
	d.Collector = &ghsource.Collector{Store: closed, GH: unknownBudget{}, Cfg: d.Cfg, Now: time.Now}
	s := New(d)
	for range 10 {
		s.jobs["fast"].once(context.Background(), t.Logf, func(string, string) {})
	}
	if n := p.calls.Load(); n != 0 {
		t.Fatalf("10 fast cycles ran %d home checks", n)
	}
}

func TestSessionStartNewHomeRechecks(t *testing.T) {
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
	waitCalls(t, p, 1)
	start("/h")
	start("")
	time.Sleep(20 * time.Millisecond)
	if n := p.calls.Load(); n != 1 {
		t.Fatalf("a covered home or none ran %d checks in all, want 1", n)
	}
}

func TestSessionStartBurstOfNewHomesSharesOneRecheck(t *testing.T) {
	s, _ := agentServer(t)
	p := &homeProbe{release: make(chan struct{})}
	s.deps.Homes = p.check
	s.noteSessionHome("/h0")
	waitCalls(t, p, 1)
	for _, h := range []string{"/h1", "/h2", "/h3", "/h4"} {
		s.noteSessionHome(h)
	}
	close(p.release)
	waitCalls(t, p, 2)
	time.Sleep(30 * time.Millisecond)
	if n := p.calls.Load(); n != 2 {
		t.Fatalf("a burst of 4 new homes ran %d checks in all, want 2", n)
	}
}

func TestStaleHomesRecheckOnRead(t *testing.T) {
	d := testDeps(t)
	now := time.Now()
	var mu sync.Mutex
	d.Now = func() time.Time { mu.Lock(); defer mu.Unlock(); return now }
	p := &homeProbe{}
	d.Homes = p.check
	s := New(d)
	s.refreshHomes(context.Background())
	s.homeWarnings()
	time.Sleep(20 * time.Millisecond)
	if n := p.calls.Load(); n != 1 {
		t.Fatalf("a fresh result was rechecked: %d checks", n)
	}

	mu.Lock()
	now = now.Add(HomesMaxAge + time.Minute)
	mu.Unlock()
	p.release = make(chan struct{})
	var wg sync.WaitGroup
	for range 5 {
		wg.Go(func() { s.homeWarnings() })
	}
	wg.Wait()
	waitCalls(t, p, 2)
	close(p.release)
	time.Sleep(20 * time.Millisecond)
	if n := p.calls.Load(); n != 2 {
		t.Fatalf("5 concurrent reads ran %d checks in all, want 2 (the first plus 1 shared)", n)
	}
}
