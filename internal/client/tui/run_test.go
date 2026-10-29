package tui

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/mattn/go-runewidth"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/api/client"
	"github.com/wloophq/wloop/internal/domain/unit"
)

type fakeDaemon struct {
	mu         sync.Mutex
	rows       []api.Row
	down       bool
	panics     bool
	noEvents   bool
	actions    []string
	actionErr  []error // ctx.Err() seen by each action when it returned
	hold       chan struct{}
	stream     chan api.Change
	rowsAt     []time.Time
	eventsAt   []time.Time
	actionsAt  []time.Time
	sources    []string
	rev        uint64
	delta      []api.Row
	removed    []unit.Unit
	rowsSince  []api.Since
	eventsFrom []api.Since
	healthAt   []time.Time

	specs        api.ActionsResponse
	specsErr     error
	specsFetched int

	plan      []api.PlanItem
	planErr   error
	planReads int
	closed    []api.ClosedSession
	planActs  []api.PlanActRequest

	plugins    []api.Plugin
	pluginsErr error

	viewsCat *api.ViewsCatalog
	// gates are the open gates.
	gates []api.GateView
	// tasks are each view's tasks; a page since the fake's position
	// carries taskDelta of its view instead, once.
	tasks       map[string][]api.TaskView
	taskRev     uint64
	taskDelta   map[string][]api.TaskView
	counters    api.Counters
	taskAsks    []taskAsk
	counterAsks [][]string
	drafts      []api.Draft
	taskDos     []taskDo
	resolves    []gateResolve
	// doLag holds TaskDo's reply after the verb is recorded, as a slow daemon does.
	doLag        time.Duration
	captures     []string
	taskReads    []string
	keysFetched  int
	catalogReads int
	pluginReads  int

	review      *api.Review
	reviewReads int
	stepsDone   []string
}

type taskAsk struct {
	view, area string
	since      api.Since
}

type gateResolve struct {
	id             int64
	intent, source string
	args           map[string]string
}

type taskDo struct {
	id, verb, source string
	args             map[string]string
}

func (f *fakeDaemon) Plugins(context.Context) ([]api.Plugin, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pluginReads++
	return slices.Clone(f.plugins), f.pluginsErr
}

// Views fails as a daemon without a store does unless viewsCat is set.
func (f *fakeDaemon) Views(context.Context) (api.ViewsCatalog, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.viewsCat == nil {
		return api.ViewsCatalog{}, errors.New("the tasks are not served: the daemon runs without a store")
	}
	return *f.viewsCat, nil
}

func (f *fakeDaemon) TasksSince(_ context.Context, q client.TaskQuery, since api.Since) (api.TasksPage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.taskAsks = append(f.taskAsks, taskAsk{q.View, q.Area, since})
	if f.down {
		return api.TasksPage{}, errors.New("connection refused")
	}
	if since == (api.Since{}) || since.Epoch != "g1" {
		return api.TasksPage{Epoch: "g1", Rev: f.taskRev, Full: true, Tasks: slices.Clone(f.tasks[q.View]), Removed: []string{}, Counters: f.counters}, nil
	}
	page := api.TasksPage{Epoch: "g1", Rev: f.taskRev, Tasks: f.taskDelta[q.View], Removed: []string{}, Counters: f.counters}
	delete(f.taskDelta, q.View)
	return page, nil
}

// Counters counts each view's tasks holding every term.
func (f *fakeDaemon) Counters(_ context.Context, area string, terms ...string) (api.Counters, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.counterAsks = append(f.counterAsks, terms)
	if f.down {
		return api.Counters{}, errors.New("connection refused")
	}
	c := api.Counters{Area: area, Views: map[string]int{}}
	for v, es := range f.tasks {
		c.Views[v] = 0
		for _, e := range es {
			if api.TaskMatches(e, terms) {
				c.Views[v]++
			}
		}
	}
	return c, nil
}

func (f *fakeDaemon) Task(_ context.Context, id string) (api.TaskView, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.taskReads = append(f.taskReads, id)
	for _, es := range f.tasks {
		for _, e := range es {
			if e.ID == id {
				return e, nil
			}
		}
	}
	return api.TaskView{}, errors.New("no entry " + id)
}

func (f *fakeDaemon) TaskDo(_ context.Context, id, verb string, req api.TaskRequest) (api.TaskResult, error) {
	f.mu.Lock()
	f.taskDos = append(f.taskDos, taskDo{id, verb, req.Source, req.Args})
	lag := f.doLag
	f.mu.Unlock()
	time.Sleep(lag)
	return api.TaskResult{Lines: []string{verb + " done"}}, nil
}

func (f *fakeDaemon) ResolveGate(_ context.Context, id int64, intent string, req api.TaskRequest) (api.GateView, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.resolves = append(f.resolves, gateResolve{id, intent, req.Source, req.Args})
	return api.GateView{ID: id, Status: "approved"}, nil
}

func (f *fakeDaemon) TaskDrafts(_ context.Context, task string) ([]api.Draft, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []api.Draft
	for _, d := range f.drafts {
		if d.Task == task {
			out = append(out, d)
		}
	}
	return out, nil
}

func (f *fakeDaemon) CreateTask(_ context.Context, req api.TaskCreate) (api.TaskView, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.captures = append(f.captures, req.Title)
	return api.TaskView{ID: "e-new", Title: req.Title}, nil
}

func (f *fakeDaemon) Keys(_ context.Context, surface string) (api.KeysResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.keysFetched++
	if surface != "tui" {
		return api.KeysResponse{}, errors.New("surface " + surface)
	}
	return api.KeysResponse{Bindings: servedKeys(f.specs)}, nil
}

// changeTasks makes the daemon hold delta for view at the next position
// and announces it with the counters, as the tasks feed does.
func (f *fakeDaemon) changeTasks(view string, delta []api.TaskView, c api.Counters) {
	f.mu.Lock()
	f.taskRev++
	if f.taskDelta == nil {
		f.taskDelta = map[string][]api.TaskView{}
	}
	f.taskDelta[view] = delta
	f.counters = c
	s, rev := f.stream, f.rev
	f.mu.Unlock()
	s <- api.Change{Type: "changed", Epoch: "e1", Rev: rev, Counters: &c}
}

func (f *fakeDaemon) Plan(_ context.Context, q api.PlanQuery) (api.PlanResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.planReads++
	res := api.PlanResponse{Items: slices.Clone(f.plan), Errors: []string{}}
	if q.Closed && q.Trigger == "" {
		res.Closed = slices.Clone(f.closed)
	}
	return res, f.planErr
}

func (f *fakeDaemon) ActOnPlan(_ context.Context, req api.PlanActRequest) (api.PlanResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.planActs = append(f.planActs, req)
	res := api.PlanResponse{Items: []api.PlanItem{}, Errors: []string{}}
	for _, id := range req.IDs {
		i := slices.IndexFunc(f.plan, func(it api.PlanItem) bool { return it.ID == id })
		if i < 0 {
			res.Errors = append(res.Errors, id+": gone")
			continue
		}
		it := f.plan[i]
		f.plan = slices.Delete(f.plan, i, i+1)
		it.Decision, it.Reason = "run", ""
		if req.Action == "dismiss" {
			it.Decision = "dismissed"
		}
		res.Items = append(res.Items, it)
	}
	return res, nil
}

func (f *fakeDaemon) RowsSince(_ context.Context, since api.Since) (api.RowsPage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.panics {
		panic("rows exploded")
	}
	f.rowsAt = append(f.rowsAt, time.Now())
	f.rowsSince = append(f.rowsSince, since)
	if f.down {
		return api.RowsPage{}, errors.New("connection refused")
	}
	if since == (api.Since{}) || since.Epoch != "e1" {
		return api.RowsPage{Epoch: "e1", Rev: f.rev, Full: true, Rows: slices.Clone(f.rows), Removed: []unit.Unit{}}, nil
	}
	page := api.RowsPage{Epoch: "e1", Rev: f.rev, Rows: f.delta, Removed: f.removed}
	f.delta, f.removed = nil, nil
	return page, nil
}

// change makes the daemon hold the rows of a delta at the next revision.
func (f *fakeDaemon) change(rows []api.Row, removed ...unit.Unit) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rev++
	f.delta, f.removed = rows, removed
}

func (f *fakeDaemon) Catalog(context.Context) (api.CatalogResponse, error) {
	f.mu.Lock()
	f.catalogReads++
	f.mu.Unlock()
	tui := []string{"tui"}
	return api.CatalogResponse{Version: "c1", Views: []api.ViewSpec{
		{ID: "needs-me", Label: "Needs me", Rows: true, Surfaces: tui},
		{ID: "mine", Label: "My PRs", Rows: true, Order: 1, Surfaces: tui},
		{ID: "loop", Label: "In loop", Rows: true, Order: 2, Surfaces: tui},
	}}, nil
}

func (f *fakeDaemon) Gates(context.Context) ([]api.GateView, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]api.GateView{}, f.gates...), nil
}

func (f *fakeDaemon) Health(context.Context) (api.Health, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.healthAt = append(f.healthAt, time.Now())
	return api.Health{}, nil
}

func (f *fakeDaemon) Actions(context.Context) (api.ActionsResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.specsFetched++
	return f.specs, f.specsErr
}

func (f *fakeDaemon) Ask(ctx context.Context, u unit.Unit, action string, req api.ActionRequest) (api.ActionResult, error) {
	f.mu.Lock()
	hold := f.hold
	f.mu.Unlock()
	if hold != nil {
		<-hold
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.actions = append(f.actions, fmt.Sprintf("%s %s %s", action, u.Key, req.Guidance))
	f.actionErr = append(f.actionErr, ctx.Err())
	f.actionsAt = append(f.actionsAt, time.Now())
	f.sources = append(f.sources, req.Source)
	if action == "review" {
		return api.ActionResult{OK: true, Intake: &api.Intake{Seq: 42, State: api.IntakeAccepted}}, nil
	}
	return api.ActionResult{OK: true, Output: []string{"ok"}}, nil
}

// Events opens a new stream on every call, as the daemon does.
func (f *fakeDaemon) Events(_ context.Context, _ string, since api.Since) (<-chan api.Change, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.eventsFrom = append(f.eventsFrom, since)
	f.eventsAt = append(f.eventsAt, time.Now())
	if f.noEvents || f.down {
		return nil, errors.New("daemon: events: 404 Not Found")
	}
	f.stream = make(chan api.Change)
	return f.stream, nil
}

func (f *fakeDaemon) announce() {
	f.mu.Lock()
	s, rev := f.stream, f.rev
	f.mu.Unlock()
	s <- api.Change{Type: "changed", Epoch: "e1", Rev: rev}
}

func (f *fakeDaemon) announceHealth() {
	f.mu.Lock()
	s, rev := f.stream, f.rev
	f.mu.Unlock()
	s <- api.Change{Type: "changed", Epoch: "e1", Rev: rev, Health: true}
}

func (f *fakeDaemon) endStream() {
	f.mu.Lock()
	defer f.mu.Unlock()
	close(f.stream)
	f.stream = nil
}

func (f *fakeDaemon) set(down bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.down = down
}

func (f *fakeDaemon) calls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.actions)
}

func (f *fakeDaemon) planCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.planReads
}

func (f *fakeDaemon) times(which *[]time.Time) []time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(*which)
}

type fakeTerm struct {
	keys       chan Key
	mu         sync.Mutex
	last       []string
	draws      int
	widest     int
	suspended  int
	suspendErr error
	reads      uint64
}

func (t *fakeTerm) Size() (int, int) { return 120, 12 }
func (t *fakeTerm) Keys() <-chan Key { return t.keys }
func (t *fakeTerm) Draw(lines []string) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.last = slices.Clone(lines)
	t.draws++
	for _, l := range lines {
		t.widest = max(t.widest, runewidth.StringWidth(l))
	}
	return nil
}
func (t *fakeTerm) Suspend() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.suspended++
	return t.suspendErr
}

// press sends keys as separate reads, as a person types them.
func (t *fakeTerm) press(keys ...Key) {
	for _, k := range keys {
		t.mu.Lock()
		t.reads++
		k.Read = t.reads
		t.mu.Unlock()
		t.keys <- k
	}
}
func (t *fakeTerm) screen() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return strings.Join(t.last, "\n")
}
func (t *fakeTerm) stats() (draws, widest, suspended int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.draws, t.widest, t.suspended
}

// eventually waits for cond on the synctest clock: it checks once every
// goroutine of the test waits, and the deadline passes in fake time only.
func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(time.Minute)
	for {
		synctest.Wait()
		if cond() {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func start(t *testing.T, d *fakeDaemon, tune ...func(*Config)) (*fakeTerm, chan error) {
	t.Helper()
	if d.specs.Version == "" {
		d.specs = servedSpecs(t)
	}
	for i := range d.rows {
		d.rows[i].SpecsVersion = d.specs.Version
		if d.rows[i].Actions == nil {
			d.rows[i] = offering(d.rows[i], "review")
		}
	}
	term := &fakeTerm{keys: make(chan Key)}
	done := make(chan error, 1)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	cfg := Config{Daemon: d, Term: term, Now: func() time.Time { return now }, Tick: 10 * time.Millisecond, Refresh: time.Hour, Reconnect: time.Hour}
	for _, f := range tune {
		f(&cfg)
	}
	go func() { done <- Run(ctx, cfg) }()
	return term, done
}

func quit(t *testing.T, term *fakeTerm, done chan error) {
	t.Helper()
	term.press(Key{Rune: 'q'})
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func oneRow() []api.Row {
	return []api.Row{row(7, "Batch export", "review", "needs-me")}
}

func TestRunActsRefreshesAndQuits(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		d := &fakeDaemon{rows: oneRow()}
		term, done := start(t, d)
		eventually(t, "the row", func() bool { return strings.Contains(term.screen(), "Batch export") })
		synctest.Wait()
		pulled := len(d.times(&d.rowsAt))
		term.press(Key{Rune: 'V'}, Key{Rune: 'x'}, Key{Name: KeyEnter})
		eventually(t, "the review", func() bool { return slices.Equal(d.calls(), []string{"review acme/api#7 x"}) })
		eventually(t, "the status", func() bool { return strings.Contains(term.screen(), "review acme/api#7: accepted as intake 42") })
		d.mu.Lock()
		sources := slices.Clone(d.sources)
		d.mu.Unlock()
		if !slices.Equal(sources, []string{"tui"}) {
			t.Fatalf("sources %q", sources)
		}
		eventually(t, "a refresh after the action", func() bool { return len(d.times(&d.rowsAt)) > pulled })
		if _, widest, _ := term.stats(); widest > 119 {
			t.Fatalf("a line reached the last column: %d cells", widest)
		}
		quit(t, term, done)
	})
}

func TestRunSurvivesTheDaemonGoingAway(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		d := &fakeDaemon{rows: oneRow()}
		term, done := start(t, d)
		eventually(t, "the rows", func() bool { return strings.Contains(term.screen(), "Batch export") })
		d.set(true)
		d.announce()
		eventually(t, "the error", func() bool { return strings.Contains(term.screen(), "daemon not answering: connection refused") })
		if !strings.Contains(term.screen(), "Batch export") {
			t.Fatal("the last rows left the screen")
		}
		d.set(false)
		d.announce()
		eventually(t, "the recovery", func() bool { return !strings.Contains(term.screen(), "not answering") })
		term.press(Key{Name: KeyCtrlC})
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	})
}

func TestRunReconnectsAfterTheStreamEndsButNotAtOnce(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const wait = 60 * time.Millisecond
		d := &fakeDaemon{rows: oneRow()}
		term, done := start(t, d, func(c *Config) { c.Reconnect = wait })
		eventually(t, "the stream", func() bool { return len(d.times(&d.eventsAt)) == 1 })
		ended := time.Now()
		d.endStream()
		eventually(t, "the reconnection", func() bool { return len(d.times(&d.eventsAt)) == 2 })
		if gap := d.times(&d.eventsAt)[1].Sub(ended); gap < wait {
			t.Fatalf("reconnected after %v, before %v", gap, wait)
		}
		time.Sleep(4 * wait)
		if n := len(d.times(&d.eventsAt)); n != 2 {
			t.Fatalf("%d streams while one stayed open", n)
		}
		quit(t, term, done)
	})
}

func TestTUINoTickWhileConnected(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		d := &fakeDaemon{rows: oneRow()}
		term, done := start(t, d, func(c *Config) { c.Refresh = 10 * time.Millisecond })
		eventually(t, "the rows", func() bool { return strings.Contains(term.screen(), "Batch export") })
		time.Sleep(300 * time.Millisecond)
		if n, h := len(d.times(&d.rowsAt)), len(d.times(&d.healthAt)); n != 1 || h != 1 {
			t.Fatalf("%d rows and %d health requests with the stream up and nothing announced", n, h)
		}
		quit(t, term, done)
	})
}

func TestTUIOneRequestPerChange(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		d := &fakeDaemon{rows: oneRow()}
		term, done := start(t, d)
		eventually(t, "the rows", func() bool { return strings.Contains(term.screen(), "Batch export") })
		eventually(t, "the stream", func() bool { return len(d.times(&d.eventsAt)) == 1 })
		changed := row(7, "Batch export v2", "review", "needs-me")
		changed.SpecsVersion = d.specs.Version
		changed = offering(changed, "review")
		d.change([]api.Row{changed})
		d.announce()
		eventually(t, "the delta", func() bool { return strings.Contains(term.screen(), "Batch export v2") })
		d.mu.Lock()
		got, health := slices.Clone(d.rowsSince), len(d.healthAt)
		d.mu.Unlock()
		if want := []api.Since{{}, {Epoch: "e1", Rev: 0}}; !slices.Equal(got, want) || health != 1 {
			t.Fatalf("rows asked since %v, %d health requests", got, health)
		}
		d.change(nil, changed.Unit)
		d.announce()
		eventually(t, "the removal", func() bool { return !strings.Contains(term.screen(), "Batch export") })
		quit(t, term, done)
	})
}

func TestTUIHealthOnlyChangeReadsNoRows(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		d := &fakeDaemon{rows: oneRow()}
		term, done := start(t, d)
		eventually(t, "the rows", func() bool { return strings.Contains(term.screen(), "Batch export") })
		eventually(t, "the stream", func() bool { return len(d.times(&d.eventsAt)) == 1 })
		d.announceHealth()
		eventually(t, "the health", func() bool { return len(d.times(&d.healthAt)) == 2 })
		if n := len(d.times(&d.rowsAt)); n != 1 {
			t.Fatalf("%d rows requests for a health-only change", n)
		}
		d.change(nil)
		d.announceHealth()
		eventually(t, "the rows past the held revision", func() bool { return len(d.times(&d.rowsAt)) == 2 })
		quit(t, term, done)
	})
}

func TestTUIResumesFromRev(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		d := &fakeDaemon{rows: oneRow()}
		term, done := start(t, d, func(c *Config) { c.Reconnect = 10 * time.Millisecond })
		eventually(t, "the stream", func() bool { return len(d.times(&d.eventsAt)) == 1 })
		// The first load pulls after the stream opens; a change before it would
		// fold into that pull and leave nothing to resume from.
		eventually(t, "the first load", func() bool { return d.planCount() == 1 })
		d.change(nil)
		d.announce()
		// The load asks for the plan after it holds the pulled revision.
		eventually(t, "the second load", func() bool { return len(d.times(&d.rowsAt)) == 2 && d.planCount() == 2 })
		d.endStream()
		eventually(t, "the reconnection", func() bool { return len(d.times(&d.eventsAt)) == 2 })
		d.mu.Lock()
		from := slices.Clone(d.eventsFrom)
		d.mu.Unlock()
		if want := []api.Since{{}, {Epoch: "e1", Rev: 1}}; !slices.Equal(from, want) {
			t.Fatalf("streams opened from %v, want %v", from, want)
		}
		quit(t, term, done)
	})
}

func TestRunPollsADaemonWithoutAnEventStream(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		d := &fakeDaemon{rows: oneRow(), noEvents: true}
		term, done := start(t, d, func(c *Config) {
			c.Reconnect = 10 * time.Millisecond
			c.Refresh = 100 * time.Millisecond
		})
		eventually(t, "the rows", func() bool { return strings.Contains(term.screen(), "Batch export") })
		time.Sleep(400 * time.Millisecond)
		if n := len(d.times(&d.eventsAt)); n > 7 {
			t.Fatalf("asked for the stream %d times in 400ms: it reconnects instead of polling", n)
		}
		if n := len(d.times(&d.rowsAt)); n < 4 {
			t.Fatalf("read the rows %d times", n)
		}
		quit(t, term, done)
	})
}

func TestRunWaitsForARunningActionBeforeLeaving(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		d := &fakeDaemon{rows: oneRow(), hold: make(chan struct{})}
		term, done := start(t, d)
		eventually(t, "the row", func() bool { return strings.Contains(term.screen(), "Batch export") })
		term.press(Key{Rune: 'V'}, Key{Name: KeyEnter}, Key{Rune: 'q'})
		eventually(t, "the wait", func() bool { return strings.Contains(term.screen(), "waiting for 1 running action") })
		select {
		case <-done:
			t.Fatal("left with an action running")
		case <-time.After(50 * time.Millisecond):
		}
		close(d.hold)
		if err := <-done; err != nil {
			t.Fatal(err)
		}
		d.mu.Lock()
		defer d.mu.Unlock()
		if len(d.actionErr) != 1 || d.actionErr[0] != nil {
			t.Fatalf("the action's context: %v", d.actionErr)
		}
	})
}

func TestRunASecondQLeavesWithoutTheAction(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		d := &fakeDaemon{rows: oneRow(), hold: make(chan struct{})}
		term, done := start(t, d)
		eventually(t, "the row", func() bool { return strings.Contains(term.screen(), "Batch export") })
		term.press(Key{Rune: 'V'}, Key{Name: KeyEnter}, Key{Rune: 'q'}, Key{Rune: 'q'})
		if err := <-done; err != nil {
			t.Fatal(err)
		}
		close(d.hold)
		eventually(t, "the action", func() bool { return len(d.calls()) == 1 })
		d.mu.Lock()
		defer d.mu.Unlock()
		if d.actionErr[0] != nil {
			t.Fatalf("leaving cancelled the action on its way to the daemon: %v", d.actionErr[0])
		}
	})
}

func TestRunSuspendsOnCtrlZ(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		term, done := start(t, &fakeDaemon{})
		term.press(Key{Name: KeyCtrlZ})
		eventually(t, "the suspend", func() bool { _, _, s := term.stats(); return s == 1 })
		quit(t, term, done)
	})
}

func TestRunReturnsAPanicAsAnError(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		_, done := start(t, &fakeDaemon{panics: true})
		if err := <-done; err == nil || !strings.Contains(err.Error(), "rows exploded") {
			t.Fatalf("%v", err)
		}
	})
}

func TestRunEndsWhenTheKeysClose(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		term, done := start(t, &fakeDaemon{})
		eventually(t, "a draw", func() bool { n, _, _ := term.stats(); return n > 0 })
		close(term.keys)
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	})
}

func TestRunKeepsCountingTheActionsItWaitsFor(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		d := &fakeDaemon{rows: append(oneRow(), row(8, "Cache warmup", "review", "needs-me")), hold: make(chan struct{})}
		term, done := start(t, d)
		eventually(t, "the rows", func() bool { return strings.Contains(term.screen(), "Cache warmup") })
		term.press(Key{Rune: 'V'}, Key{Name: KeyEnter}, Key{Name: KeyDown}, Key{Rune: 'V'}, Key{Name: KeyEnter}, Key{Rune: 'q'})
		eventually(t, "two", func() bool { return strings.Contains(term.screen(), "waiting for 2 running action(s) to finish") })
		d.hold <- struct{}{}
		eventually(t, "one", func() bool {
			return strings.Contains(term.screen(), "waiting for 1 running action(s) to finish; q again")
		})
		d.hold <- struct{}{}
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	})
}

func TestRunEndsWhenTheTerminalCannotBeTakenBack(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		term, done := start(t, &fakeDaemon{})
		term.mu.Lock()
		term.suspendErr = errors.New("tcsetattr: bad file descriptor")
		term.mu.Unlock()
		term.press(Key{Name: KeyCtrlZ})
		if err := <-done; err == nil || !strings.Contains(err.Error(), "suspend: tcsetattr") {
			t.Fatalf("%v", err)
		}
	})
}

func TestSpecsAreFetchedOnceAndAgainWhenTheirVersionChanges(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		d := &fakeDaemon{rows: oneRow()}
		term, done := start(t, d)
		eventually(t, "the row", func() bool { return strings.Contains(term.screen(), "Batch export") })
		d.announce()
		eventually(t, "a refresh", func() bool { return len(d.times(&d.rowsAt)) >= 2 })
		d.announce()
		eventually(t, "another refresh", func() bool { return len(d.times(&d.rowsAt)) >= 3 })
		d.mu.Lock()
		fetched := d.specsFetched
		d.mu.Unlock()
		if fetched != 1 {
			t.Fatalf("specs fetched %d times at one version", fetched)
		}
		d.mu.Lock()
		d.specs.Version = "next"
		d.rows[0].SpecsVersion = "next"
		d.mu.Unlock()
		d.change(slices.Clone(d.rows))
		d.announce()
		eventually(t, "the new version", func() bool {
			d.mu.Lock()
			defer d.mu.Unlock()
			return d.specsFetched == 2
		})
		quit(t, term, done)
	})
}

func TestAFailingSpecsReadKeepsTheFreshRows(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		d := &fakeDaemon{rows: oneRow()}
		term, done := start(t, d)
		eventually(t, "the row", func() bool { return strings.Contains(term.screen(), "Batch export") })
		d.mu.Lock()
		d.specsErr = errors.New("actions down")
		d.specs.Version = "next"
		d.rows[0].SpecsVersion = "next"
		d.rows[0].Title = "Fresh title"
		d.mu.Unlock()
		d.change(slices.Clone(d.rows))
		d.announce()
		eventually(t, "the fresh row and the status", func() bool {
			s := term.screen()
			return strings.Contains(s, "Fresh title") && strings.Contains(s, "actions not refreshed: actions down")
		})
		if strings.Contains(term.screen(), "not answering") {
			t.Fatal("a specs failure was shown as a daemon failure")
		}
		term.press(Key{Rune: 'V'}, Key{Name: KeyEnter})
		eventually(t, "the review still works with the previous specs", func() bool { return len(d.calls()) == 1 })
		quit(t, term, done)
	})
}
