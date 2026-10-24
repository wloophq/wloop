package workspace_test

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/domain/turn"
	wsdomain "github.com/wloophq/wloop/internal/domain/workspace"
	"github.com/wloophq/wloop/internal/meter"
	"github.com/wloophq/wloop/internal/meter/metertest"
	"github.com/wloophq/wloop/internal/workspace"
)

// observer serves its fields and meters every call into the context it is called with.
type observer struct {
	// ctx carries the test's meter scope; tests read the cache with it.
	ctx context.Context

	mu         sync.Mutex
	repos      []wsdomain.Repo
	workspaces []wsdomain.Workspace
	panes      []wsdomain.Pane
	terminals  []wsdomain.Terminal
	runtime    string
	// termRuntime, when set, is the runtime id the terminal listing answers with.
	termRuntime string
	err         error
	// gate, when set, holds every call, after it read the fields, until a token arrives.
	gate    chan struct{}
	started chan string
}

func newObserver(t *testing.T) (*observer, *meter.Scope) {
	ctx, sc := metertest.Start(t, context.Background())
	return &observer{
		ctx:        ctx,
		repos:      []wsdomain.Repo{{ID: "r1"}},
		workspaces: []wsdomain.Workspace{{ID: "a", Status: "todo"}, {ID: "b", Status: "todo"}},
		panes: []wsdomain.Pane{
			{WorkspaceID: "a", Pane: "t:1", State: turn.AgentWorking, LastMessage: "hi"},
			{WorkspaceID: "b", Pane: "t:1", State: turn.AgentDone},
		},
		terminals: []wsdomain.Terminal{{Handle: "h1", WorkspaceID: "a", Usable: true}},
		runtime:   "r1",
		started:   make(chan string, 1000),
	}, sc
}

type listing struct {
	repos       []wsdomain.Repo
	workspaces  []wsdomain.Workspace
	panes       []wsdomain.Pane
	terminals   []wsdomain.Terminal
	runtime     string
	termRuntime string
}

func (o *observer) call(ctx context.Context, op string) (snap listing, err error) {
	meter.Add(ctx, meter.Sample{Res: meter.Workspace, Op: op})
	o.mu.Lock()
	snap = listing{repos: slices.Clone(o.repos), workspaces: slices.Clone(o.workspaces),
		panes: slices.Clone(o.panes), terminals: slices.Clone(o.terminals), runtime: o.runtime, termRuntime: o.termRuntime}
	err, gate := o.err, o.gate
	o.mu.Unlock()
	o.started <- op
	if gate != nil {
		<-gate
	}
	return snap, err
}

func (o *observer) set(f func(o *observer)) {
	o.mu.Lock()
	defer o.mu.Unlock()
	f(o)
}

func (o *observer) scope(runtime string) wsdomain.Scope {
	return wsdomain.Scope{Hosts: []string{wsdomain.LocalHost}, Complete: true, RuntimeID: runtime}
}

func (o *observer) Repos(ctx context.Context) ([]wsdomain.Repo, error) {
	s, err := o.call(ctx, "repos")
	return s.repos, err
}

func (o *observer) Workspaces(ctx context.Context) ([]wsdomain.Workspace, wsdomain.Scope, error) {
	s, err := o.call(ctx, "workspaces")
	return s.workspaces, o.scope(s.runtime), err
}

func (o *observer) Panes(ctx context.Context) ([]wsdomain.Pane, error) {
	s, err := o.call(ctx, "panes")
	return s.panes, err
}

func (o *observer) AllTerminals(ctx context.Context) ([]wsdomain.Terminal, wsdomain.Scope, error) {
	s, err := o.call(ctx, "terminals")
	if s.termRuntime != "" {
		return s.terminals, o.scope(s.termRuntime), err
	}
	return s.terminals, o.scope(s.runtime), err
}

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func newClock() *clock { return &clock{t: time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)} }

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

func loads(sc *meter.Scope, op string) int64 {
	return metertest.Count(sc, metertest.Limit{Res: meter.Workspace, Op: op})
}

func mustRead(t *testing.T, ctx context.Context, c *workspace.Cache, p wsdomain.Part) *wsdomain.Snapshot {
	t.Helper()
	s, err := c.Read(ctx, p)
	if err != nil {
		t.Fatalf("Read(%b): %v", p, err)
	}
	return s
}

func TestCacheSingleflight(t *testing.T) {
	o, sc := newObserver(t)
	o.gate = make(chan struct{})
	c := workspace.NewCache(o, workspace.DefaultMaxAge(2*time.Minute), newClock().Now)
	var wg sync.WaitGroup
	errs := make(chan error, 50)
	for range 50 {
		wg.Go(func() {
			s, err := c.Read(o.ctx, wsdomain.PartPanes)
			if err == nil && len(s.Panes) != 2 {
				err = errors.New("snapshot without the panes")
			}
			errs <- err
		})
	}
	<-o.started
	close(o.gate)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	mustRead(t, o.ctx, c, wsdomain.PartPanes)
	if n := loads(sc, "panes"); n != 1 {
		t.Fatalf("panes loads = %d, want 1", n)
	}
	metertest.Within(t, sc, metertest.Budget{{Res: meter.Workspace}: 1})
}

func TestCacheMaxAge(t *testing.T) {
	o, sc := newObserver(t)
	clk := newClock()
	c := workspace.NewCache(o, workspace.DefaultMaxAge(30*time.Second), clk.Now)
	mustRead(t, o.ctx, c, wsdomain.PartPanes)
	clk.Advance(29 * time.Second)
	mustRead(t, o.ctx, c, wsdomain.PartPanes)
	if n := loads(sc, "panes"); n != 1 {
		t.Fatalf("at 29 s: panes loads = %d, want 1", n)
	}
	clk.Advance(2 * time.Second)
	mustRead(t, o.ctx, c, wsdomain.PartPanes)
	if n := loads(sc, "panes"); n != 2 {
		t.Fatalf("at 31 s: panes loads = %d, want 2", n)
	}
	if got := workspace.DefaultMaxAge(time.Minute); got[wsdomain.PartRepos] != 10*time.Minute ||
		got[wsdomain.PartWorkspaces] != time.Minute || got[wsdomain.PartPanes] != time.Minute || got[wsdomain.PartTerminals] != time.Minute {
		t.Fatalf("DefaultMaxAge = %v", got)
	}
}

func TestCacheInvalidateNeverJoinsOlderLoad(t *testing.T) {
	o, sc := newObserver(t)
	o.gate = make(chan struct{}, 10)
	clk := newClock()
	c := workspace.NewCache(o, workspace.DefaultMaxAge(2*time.Minute), clk.Now)
	first := make(chan error, 1)
	go func() {
		_, err := c.Read(o.ctx, wsdomain.PartPanes)
		first <- err
	}()
	<-o.started
	clk.Advance(time.Second)
	invalidatedAt := clk.Now()
	c.Invalidate(wsdomain.PartPanes)
	o.set(func(o *observer) { o.panes = []wsdomain.Pane{{WorkspaceID: "a", Pane: "t:1", State: turn.AgentDone}} })
	clk.Advance(time.Second)
	second := make(chan *wsdomain.Snapshot, 1)
	go func() {
		s, _ := c.Read(o.ctx, wsdomain.PartPanes)
		second <- s
	}()
	o.gate <- struct{}{}
	<-o.started
	o.gate <- struct{}{}
	s := <-second
	if err := <-first; err != nil {
		t.Fatal(err)
	}
	if len(s.Panes) != 1 || !s.ReadAt[wsdomain.PartPanes].After(invalidatedAt) {
		t.Fatalf("read joined the load that began before Invalidate: panes %v, read at %v", s.Panes, s.ReadAt)
	}
	if n := loads(sc, "panes"); n != 2 {
		t.Fatalf("panes loads = %d, want 2", n)
	}
}

func TestCacheGenOnlyOnProjectionChange(t *testing.T) {
	o, _ := newObserver(t)
	c := workspace.NewCache(o, workspace.DefaultMaxAge(2*time.Minute), newClock().Now)
	reload := func() uint64 {
		c.Invalidate(wsdomain.PartAll)
		return mustRead(t, o.ctx, c, wsdomain.PartAll).Gen
	}
	gen := mustRead(t, o.ctx, c, wsdomain.PartAll).Gen
	if again := reload(); again != gen {
		t.Fatalf("unchanged reload moved Gen %d -> %d", gen, again)
	}
	o.set(func(o *observer) {
		o.panes[0].LastMessage = "new text"
		o.terminals[0].LastOutput = time.Unix(100, 0)
	})
	if got := reload(); got != gen {
		t.Fatalf("repaint moved Gen %d -> %d", gen, got)
	}
	o.set(func(o *observer) { o.panes[0].State = turn.AgentWaiting })
	if got := reload(); got != gen+1 {
		t.Fatalf("state change: Gen %d, want %d", got, gen+1)
	}
	o.set(func(o *observer) { o.workspaces[1].Status = "done" })
	if got := reload(); got != gen+2 {
		t.Fatalf("column change: Gen %d, want %d", got, gen+2)
	}
}

func TestCacheRuntimeRestartInvalidatesAll(t *testing.T) {
	o, sc := newObserver(t)
	c := workspace.NewCache(o, workspace.DefaultMaxAge(2*time.Minute), newClock().Now)
	mustRead(t, o.ctx, c, wsdomain.PartAll)
	o.set(func(o *observer) { o.runtime = "r2" })
	c.Invalidate(wsdomain.PartWorkspaces)
	mustRead(t, o.ctx, c, wsdomain.PartWorkspaces)
	s := mustRead(t, o.ctx, c, wsdomain.PartAll)
	for _, op := range []string{"repos", "workspaces", "panes", "terminals"} {
		if n := loads(sc, op); n != 2 {
			t.Errorf("%s loads = %d, want 2", op, n)
		}
	}
	if s.WorkspaceScope.RuntimeID != "r2" || s.TerminalScope.RuntimeID != "r2" {
		t.Fatalf("snapshot mixes runtimes: %+v %+v", s.WorkspaceScope, s.TerminalScope)
	}
}

func TestCacheSplitRuntimeReloadsOnceAndReadsPartial(t *testing.T) {
	o, sc := newObserver(t)
	o.set(func(o *observer) { o.termRuntime = "r2" })
	c := workspace.NewCache(o, workspace.DefaultMaxAge(2*time.Minute), newClock().Now)
	ctx, cancel := context.WithTimeout(o.ctx, 5*time.Second)
	defer cancel()
	for read := int64(1); read <= 2; read++ {
		s := mustRead(t, ctx, c, wsdomain.PartAll)
		for _, op := range []string{"workspaces", "terminals"} {
			if n := loads(sc, op); n != 2*read {
				t.Fatalf("read %d: %s loads = %d, want %d", read, op, n, 2*read)
			}
		}
		if s.WorkspaceScope.Complete || s.TerminalScope.Complete {
			t.Fatalf("read %d: a split snapshot reads complete: %+v %+v", read, s.WorkspaceScope, s.TerminalScope)
		}
	}
	o.set(func(o *observer) { o.termRuntime = "" })
	s := mustRead(t, ctx, c, wsdomain.PartAll)
	if !s.WorkspaceScope.Complete || !s.TerminalScope.Complete || s.TerminalScope.RuntimeID != "r1" {
		t.Fatalf("agreeing runtimes read partial: %+v %+v", s.WorkspaceScope, s.TerminalScope)
	}
}

func TestCacheOldRuntimeAnswerAfterNewReadsPartial(t *testing.T) {
	o, _ := newObserver(t)
	c := workspace.NewCache(o, workspace.DefaultMaxAge(2*time.Minute), newClock().Now)
	mustRead(t, o.ctx, c, wsdomain.PartAll)
	for range 4 {
		<-o.started
	}

	held := make(chan struct{})
	o.set(func(o *observer) { o.gate = held })
	c.Invalidate(wsdomain.PartTerminals)
	oldDone := make(chan struct{})
	go func() {
		defer close(oldDone)
		_, _ = c.Read(o.ctx, wsdomain.PartTerminals)
	}()
	if op := <-o.started; op != "terminals" {
		t.Fatalf("started %s, want terminals", op)
	}

	o.set(func(o *observer) { o.gate, o.runtime = nil, "r2" })
	c.Invalidate(wsdomain.PartWorkspaces)
	type read struct {
		s   *wsdomain.Snapshot
		err error
	}
	got := make(chan read, 1)
	go func() {
		s, err := c.Read(o.ctx, wsdomain.PartWorkspaces|wsdomain.PartTerminals)
		got <- read{s, err}
	}()
	for deadline := time.Now().Add(5 * time.Second); c.Last().WorkspaceScope.RuntimeID != "r2"; {
		if time.Now().After(deadline) {
			t.Fatal("the new runtime's workspaces never committed")
		}
		time.Sleep(time.Millisecond)
	}

	later := make(chan struct{})
	o.set(func(o *observer) { o.gate = later })
	held <- struct{}{}
	r := <-got
	close(later)
	<-oldDone
	if r.err != nil {
		t.Fatal(r.err)
	}
	ws, ts := r.s.WorkspaceScope, r.s.TerminalScope
	if ws.RuntimeID != "r2" || ts.RuntimeID != "r1" {
		t.Fatalf("scopes %+v %+v, want workspaces from r2 and terminals from r1", ws, ts)
	}
	if ws.Complete || ts.Complete {
		t.Fatalf("a snapshot of two runtimes reads complete: %+v %+v", ws, ts)
	}
}

func TestCacheErrorKeepsLast(t *testing.T) {
	o, _ := newObserver(t)
	c := workspace.NewCache(o, workspace.DefaultMaxAge(2*time.Minute), newClock().Now)
	before := mustRead(t, o.ctx, c, wsdomain.PartAll)
	boom := errors.New("socket refused")
	o.set(func(o *observer) { o.err = boom })
	c.Invalidate(wsdomain.PartPanes)
	if _, err := c.Read(o.ctx, wsdomain.PartPanes); !errors.Is(err, boom) {
		t.Fatalf("Read error = %v, want %v", err, boom)
	}
	if c.Last() != before {
		t.Fatal("a failed load replaced the last snapshot")
	}
	o.set(func(o *observer) { o.err = nil })
	if s := mustRead(t, o.ctx, c, wsdomain.PartPanes); s == before {
		t.Fatal("the failed part was not reloaded after the error cleared")
	}
}

func TestCacheOnChangeNamesMovedWorkspaces(t *testing.T) {
	o, _ := newObserver(t)
	c := workspace.NewCache(o, workspace.DefaultMaxAge(2*time.Minute), newClock().Now)
	var calls [][]string
	c.OnChange(func(_ *wsdomain.Snapshot, moved []string) { calls = append(calls, moved) })
	mustRead(t, o.ctx, c, wsdomain.PartAll)
	calls = nil
	o.set(func(o *observer) {
		o.panes[1].State = turn.AgentWorking
		o.panes[0].LastMessage = "more text"
		o.terminals = append(o.terminals, wsdomain.Terminal{Handle: "h2", WorkspaceID: "c", Usable: true})
	})
	c.Invalidate(wsdomain.PartMutable)
	mustRead(t, o.ctx, c, wsdomain.PartMutable)
	if len(calls) != 1 || !slices.Equal(calls[0], []string{"b", "c"}) {
		t.Fatalf("OnChange calls = %v, want one with [b c]", calls)
	}
	o.set(func(o *observer) { o.panes[0].LastMessage = "even more" })
	c.Invalidate(wsdomain.PartMutable)
	mustRead(t, o.ctx, c, wsdomain.PartMutable)
	if len(calls) != 1 {
		t.Fatalf("assistant text alone called OnChange: %v", calls)
	}
}

func TestRefreshIsOneAsyncLoad(t *testing.T) {
	o, sc := newObserver(t)
	c := workspace.NewCache(o, workspace.DefaultMaxAge(2*time.Minute), newClock().Now)
	reg := meter.NewRegistry(nil)
	c.SetBase(meter.WithRegistry(o.ctx, reg))
	mustRead(t, o.ctx, c, wsdomain.PartAll)
	for len(o.started) > 0 {
		<-o.started
	}
	changed := make(chan []string, 4)
	c.OnChange(func(_ *wsdomain.Snapshot, moved []string) { changed <- moved })
	o.set(func(o *observer) {
		o.panes[0].State = turn.AgentDone
		o.gate = make(chan struct{})
	})
	c.Refresh(wsdomain.PartPanes | wsdomain.PartTerminals)
	<-o.started
	<-o.started
	close(o.gate)
	select {
	case moved := <-changed:
		if !slices.Equal(moved, []string{"a"}) {
			t.Fatalf("moved = %v, want [a]", moved)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Refresh never reported the change")
	}
	metertest.Within(t, sc, metertest.Budget{
		{Res: meter.Workspace, Op: "panes"}: 2, {Res: meter.Workspace, Op: "terminals"}: 2,
		{Res: meter.Workspace, Op: "workspaces"}: 1, {Res: meter.Workspace, Op: "repos"}: 1,
	})
	if n := loads(sc, "panes") + loads(sc, "terminals"); n != 4 {
		t.Fatalf("panes and terminals loads = %d, want 4", n)
	}
	if s := c.Last(); s.Panes[0].State != turn.AgentDone {
		t.Fatalf("Last after Refresh = %+v", s.Panes)
	}
	loop := t.Name() + "/" + meter.Loop("workspace")
	i := slices.IndexFunc(reg.Totals(), func(x meter.Total) bool { return x.Name == loop })
	if i < 0 {
		t.Fatalf("the refresh load is not metered under %s: %v", loop, reg.Totals())
	}
	metertest.WithinTally(t, reg.Totals()[i].Tally, metertest.Budget{
		{Res: meter.Workspace, Op: "panes"}: 1, {Res: meter.Workspace, Op: "terminals"}: 1, {Res: meter.Workspace}: 2,
	})
}

func TestCacheLeaderCancelKeepsSharedLoad(t *testing.T) {
	o, sc := newObserver(t)
	o.gate = make(chan struct{})
	c := workspace.NewCache(o, workspace.DefaultMaxAge(2*time.Minute), newClock().Now)
	leaderCtx, cancel := context.WithCancel(o.ctx)
	leader := make(chan error, 1)
	go func() {
		_, err := c.Read(leaderCtx, wsdomain.PartPanes)
		leader <- err
	}()
	<-o.started
	follower := make(chan *wsdomain.Snapshot, 1)
	go func() {
		s, _ := c.Read(o.ctx, wsdomain.PartPanes)
		follower <- s
	}()
	cancel()
	if err := <-leader; !errors.Is(err, context.Canceled) {
		t.Fatalf("leader Read = %v, want context.Canceled", err)
	}
	close(o.gate)
	if s := <-follower; s == nil || len(s.Panes) != 2 {
		t.Fatalf("follower got %+v", s)
	}
	if n := loads(sc, "panes"); n != 1 {
		t.Fatalf("panes loads = %d, want 1", n)
	}
}

func TestCacheRecentRefusesOldOrForeignParts(t *testing.T) {
	o, sc := newObserver(t)
	clk := newClock()
	c := workspace.NewCache(o, workspace.DefaultMaxAge(2*time.Minute), clk.Now)
	if c.Recent(wsdomain.PartPanes) != nil {
		t.Fatal("recent panes before any load")
	}
	mustRead(t, o.ctx, c, wsdomain.PartAll)
	o.set(func(o *observer) { o.err = errors.New("socket refused") })
	c.Invalidate(wsdomain.PartPanes)
	if _, err := c.Read(o.ctx, wsdomain.PartPanes); err == nil {
		t.Fatal("a failing source read the panes")
	}
	clk.Advance(4*time.Minute - time.Second)
	if c.Recent(wsdomain.PartPanes) == nil {
		t.Fatal("panes read under twice their max age are not recent")
	}
	clk.Advance(time.Second)
	calls := loads(sc, "panes")
	if c.Recent(wsdomain.PartPanes) != nil {
		t.Fatal("panes read twice their max age ago are recent")
	}
	if n := loads(sc, "panes"); n != calls {
		t.Fatalf("Recent loaded the panes: %d loads, %d before", n, calls)
	}

	o.set(func(o *observer) { o.err = nil })
	mustRead(t, o.ctx, c, wsdomain.PartAll)
	o.set(func(o *observer) { o.runtime = "r2" })
	c.Invalidate(wsdomain.PartWorkspaces)
	mustRead(t, o.ctx, c, wsdomain.PartWorkspaces)
	if c.Recent(wsdomain.PartPanes) != nil {
		t.Fatal("panes of the previous runtime are recent")
	}
	if c.Recent(wsdomain.PartWorkspaces) == nil {
		t.Fatal("workspaces the new runtime read are not recent")
	}
	mustRead(t, o.ctx, c, wsdomain.PartPanes)
	if c.Recent(wsdomain.PartPanes) == nil {
		t.Fatal("panes the new runtime read are not recent")
	}
}
