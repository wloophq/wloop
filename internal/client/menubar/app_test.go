package menubar

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/api/client"
	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/brand/brandtest"
	"github.com/wloophq/wloop/internal/domain/unit"
)

var now = time.Date(2026, 10, 14, 15, 0, 0, 0, time.UTC)

type actionCall struct {
	name string
	req  api.ActionRequest
}

type verbCall struct {
	id, verb string
	req      api.TaskRequest
}

type fakeDaemon struct {
	mu         sync.Mutex
	reads      int
	healthN    int
	sinces     []api.Since
	delta      *api.RowsPage // answers the next RowsSince when set
	events     chan api.Change
	eventsN    int
	eventsErr  error
	specReads  int
	dropped    bool // Events answers with a stream that has already ended
	rows       map[string][]api.Row
	health     api.Health
	err        error
	rowsErr    error // RowsSince answers it, the health still answering
	actions    []string
	reqs       []actionCall
	paused     []bool
	app        []api.AppStatus
	jobs       []string
	failing    error // Action and TaskDo answer it
	output     []string
	plugins    []api.Plugin // nil: github enabled with its In loop section
	pluginsN   int
	pluginsErr error // Plugins answers it

	views       []api.TaskViewSpec        // nil: Focus, Inbox and Outbox, each badged
	tasks       map[string][]api.TaskView // by view
	taskDelta   map[string]*api.TasksPage
	counters    api.Counters
	taskReads   int
	taskSinces  map[string][]api.Since
	verbs       []verbCall
	result      api.TaskResult
	taskLookups int
	digests     map[string]api.Digest
	digestDays  []string
	digestErr   error
}

func (f *fakeDaemon) RowsSince(_ context.Context, since api.Since) (api.RowsPage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reads++
	f.sinces = append(f.sinces, since)
	if f.err != nil || f.rowsErr != nil {
		return api.RowsPage{}, cmp.Or(f.err, f.rowsErr)
	}
	if p := f.delta; p != nil {
		f.delta = nil
		return *p, nil
	}
	var rows []api.Row
	at := map[unit.Unit]int{}
	for view, list := range f.rows {
		for _, r := range list {
			i, ok := at[r.Unit]
			if !ok {
				i = len(rows)
				at[r.Unit] = i
				r.Views = nil
				rows = append(rows, r)
			}
			rows[i].Views = append(rows[i].Views, view)
		}
	}
	return api.RowsPage{Epoch: "e1", Rev: uint64(f.reads), Full: true, Rows: rows, Removed: []unit.Unit{}}, nil
}

func (f *fakeDaemon) TasksSince(_ context.Context, q client.TaskQuery, since api.Since) (api.TasksPage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.taskReads++
	if f.taskSinces == nil {
		f.taskSinces = map[string][]api.Since{}
	}
	f.taskSinces[q.View] = append(f.taskSinces[q.View], since)
	if f.err != nil {
		return api.TasksPage{}, f.err
	}
	if p := f.taskDelta[q.View]; p != nil {
		delete(f.taskDelta, q.View)
		return *p, nil
	}
	if since != (api.Since{}) {
		return api.TasksPage{Epoch: "e1", Rev: uint64(f.taskReads), Tasks: []api.TaskView{}, Removed: []string{}, Counters: f.counters}, nil
	}
	return api.TasksPage{Epoch: "e1", Rev: uint64(f.taskReads), Full: true, Tasks: slices.Clone(f.tasks[q.View]), Removed: []string{}, Counters: f.counters}, nil
}

func (f *fakeDaemon) Task(_ context.Context, id string) (api.TaskView, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.taskLookups++
	for _, list := range f.tasks {
		for _, e := range list {
			if e.ID == id {
				return e, nil
			}
		}
	}
	return api.TaskView{}, errors.New("no entry " + id)
}

func (f *fakeDaemon) TaskDo(_ context.Context, id, verb string, req api.TaskRequest) (api.TaskResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.verbs = append(f.verbs, verbCall{id, verb, req})
	return f.result, f.failing
}

func (f *fakeDaemon) Digest(_ context.Context, day string) (api.Digest, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.digestDays = append(f.digestDays, day)
	return f.digests[day], f.digestErr
}

func (f *fakeDaemon) Action(_ context.Context, u unit.Unit, name string, req api.ActionRequest) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.actions = append(f.actions, strings.TrimSpace(name+" "+u.Key+" "+req.Guidance))
	f.reqs = append(f.reqs, actionCall{name, req})
	return f.output, f.failing
}

func (f *fakeDaemon) Views(context.Context) (api.ViewsCatalog, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return api.ViewsCatalog{}, f.err
	}
	if f.views != nil {
		return api.ViewsCatalog{Views: f.views}, nil
	}
	return api.ViewsCatalog{Views: []api.TaskViewSpec{{ID: "focus", Label: "Focus", Badge: true}, {ID: "inbox", Label: "Inbox", Badge: true},
		{ID: "outbox", Label: "Outbox", Over: "gates", Badge: true}}}, nil
}

func (f *fakeDaemon) Plugins(context.Context) ([]api.Plugin, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pluginsN++
	if f.pluginsErr != nil {
		return nil, f.pluginsErr
	}
	if f.plugins != nil {
		return f.plugins, f.err
	}
	return []api.Plugin{{ID: unit.GitHub, Enabled: true, Capabilities: []string{api.CapabilityDetail}, UI: &api.PluginUI{Menubar: []api.MenubarSection{{Label: "In loop", View: api.ViewLoop}}}}}, f.err
}

func (f *fakeDaemon) snapshotVerbs() []verbCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.verbs)
}

func (f *fakeDaemon) Health(context.Context) (api.Health, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.healthN++
	return f.health, f.err
}
func (f *fakeDaemon) Trigger(_ context.Context, job string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.jobs = append(f.jobs, job)
	return nil
}
func (f *fakeDaemon) SetPause(_ context.Context, req api.PauseRequest) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.paused = append(f.paused, req.Paused)
	return nil
}
func (f *fakeDaemon) ReportApp(_ context.Context, st api.AppStatus) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.app = append(f.app, st)
	return nil
}
func (f *fakeDaemon) Actions(context.Context) (api.ActionsResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.specReads++
	return goldenSpecs(), f.err
}
func (f *fakeDaemon) Events(ctx context.Context, _ string, _ api.Since) (<-chan api.Change, error) {
	ch := make(chan api.Change)
	f.mu.Lock()
	f.eventsN++
	if f.eventsErr != nil {
		f.mu.Unlock()
		return nil, f.eventsErr
	}
	dropped := f.dropped
	if f.events != nil {
		ch = f.events
	}
	f.mu.Unlock()
	if f.events != nil {
		return ch, nil
	}
	if dropped {
		close(ch)
		return ch, nil
	}
	go func() { <-ctx.Done(); close(ch) }()
	return ch, nil
}

type fakeTray struct {
	mu    sync.Mutex
	title string
	menu  []MenuItem
}

func (f *fakeTray) SetTitle(s string)    { f.mu.Lock(); f.title = s; f.mu.Unlock() }
func (f *fakeTray) SetMenu(m []MenuItem) { f.mu.Lock(); f.menu = m; f.mu.Unlock() }

type fakeNotifier struct {
	mu        sync.Mutex
	posted    []Notification
	withdrawn []string
	respond   func(Response)
	cleared   int
	perm      Permission
	postErr   error
	block     chan struct{} // Authorize waits on it when set, like the system dialog
	asked     int           // Permission calls
	cats      *Categories
}

func (f *fakeNotifier) categories() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.cats == nil {
		f.cats = NewCategories()
	}
	var out []string
	for _, c := range f.cats.List() {
		out = append(out, string(c.ID))
	}
	return out
}

func (f *fakeNotifier) categoryActions(c Category) []NotificationAction {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, spec := range f.cats.List() {
		if spec.ID == c {
			return spec.Actions
		}
	}
	return nil
}

func (f *fakeNotifier) Authorize(ctx context.Context) (Permission, error) {
	if f.block != nil {
		select {
		case <-f.block:
		case <-ctx.Done():
			return PermissionNotDetermined, ctx.Err()
		}
	}
	return f.Permission(ctx)
}

func (f *fakeNotifier) Permission(context.Context) (Permission, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.asked++
	return f.perm, nil
}

func (f *fakeNotifier) permissionCalls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.asked
}

func (f *fakeNotifier) setPerm(p Permission) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.perm = p
}
func (f *fakeNotifier) Post(n Notification) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.postErr != nil {
		return f.postErr
	}
	f.posted = append(f.posted, n)
	if f.cats == nil {
		f.cats = NewCategories()
	}
	f.cats.Post(n)
	return nil
}
func (f *fakeNotifier) Withdraw(ids []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.withdrawn = append(f.withdrawn, ids...)
	if f.cats == nil {
		f.cats = NewCategories()
	}
	f.cats.Withdraw(ids)
}
func (f *fakeNotifier) WithdrawAll() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cleared++
}
func (f *fakeNotifier) OnResponse(fn func(Response)) { f.respond = fn }

func uref(n int) unit.Unit {
	return unit.Unit{Provider: unit.GitHub, Key: fmt.Sprintf("acme/api#%d", n)}
}

func reviewRow(n int) api.Row {
	u := uref(n)
	return api.Row{Unit: u, Label: u.Key, Title: "Review me", Kind: "review", Summary: api.Summary{WaitsOn: "me", Line: "review requested", Reason: "review requested", Since: now.Add(-3 * time.Hour)}}
}

type rig struct {
	c      *Controller
	d      *fakeDaemon
	tray   *fakeTray
	notes  *fakeNotifier
	mu     sync.Mutex
	opened []string
	quit   bool
}

func (r *rig) openedURLs() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.opened)
}

func newDaemon() *fakeDaemon {
	return &fakeDaemon{rows: map[string][]api.Row{}, tasks: map[string][]api.TaskView{}, taskDelta: map[string]*api.TasksPage{}, digests: map[string]api.Digest{}}
}

func newRig(t *testing.T) *rig {
	t.Helper()
	r := &rig{d: newDaemon(), tray: &fakeTray{}, notes: &fakeNotifier{perm: PermissionAuthorized}}
	r.c = New(Config{
		Daemon: r.d, Tray: r.tray, Notifier: r.notes,
		OpenURL:    func(u string) error { r.mu.Lock(); r.opened = append(r.opened, u); r.mu.Unlock(); return nil },
		CockpitURL: func(route string) (string, error) { return "cockpit#" + route, nil },
		Quit:       func() { r.quit = true },
		Now:        func() time.Time { return now },
	})
	return r
}

func (r *rig) title() string {
	r.tray.mu.Lock()
	defer r.tray.mu.Unlock()
	return r.tray.title
}

func titles(items []MenuItem) []string {
	var out []string
	for _, it := range items {
		if it.Separator {
			out = append(out, "---")
			continue
		}
		out = append(out, it.Title)
	}
	return out
}

func find(items []MenuItem, prefix string) (MenuItem, bool) {
	for _, it := range items {
		if strings.HasPrefix(it.Title, prefix) {
			return it, true
		}
	}
	return MenuItem{}, false
}

func TestPluginsAreReadWithTheHealth(t *testing.T) {
	r := newRig(t)
	r.c.Refresh(context.Background())
	r.c.apply(context.Background(), api.Change{Epoch: "e1", Rev: 99})
	if r.d.pluginsN != 1 {
		t.Fatalf("plugins read %d times without a health change, want 1", r.d.pluginsN)
	}
	r.c.apply(context.Background(), api.Change{Health: true})
	if r.d.pluginsN != 2 {
		t.Fatalf("plugins read %d times after a health change, want 2", r.d.pluginsN)
	}
}

func TestFailedPluginsReadIsRetried(t *testing.T) {
	r := newRig(t)
	r.d.rows["loop"] = []api.Row{reviewRow(2)}
	r.d.pluginsErr = errors.New("connection reset")
	r.c.Refresh(context.Background())
	r.d.mu.Lock()
	r.d.pluginsErr = nil
	r.d.mu.Unlock()
	r.c.apply(context.Background(), api.Change{Epoch: "e1", Rev: 99})
	if _, ok := find(r.tray.menu, "In loop (1)"); !ok || r.d.pluginsN != 2 {
		t.Fatalf("plugins read %d times; menu %q", r.d.pluginsN, titles(r.tray.menu))
	}
}

func TestRowSubmenuIsTheRowsMenubarActions(t *testing.T) {
	for name, row := range goldenRows(t) {
		t.Run(name, func(t *testing.T) {
			if row.Kind == "done" {
				t.Skip("a done row is in no menu section")
			}
			r := newRig(t)
			r.d.rows["loop"] = []api.Row{row}
			r.c.Refresh(context.Background())
			it, ok := find(r.tray.menu, row.Label)
			if !ok {
				t.Fatalf("no row in %q", titles(r.tray.menu))
			}
			var want []string
			for _, st := range row.Actions {
				if spec := specOf(t, st.ID); slices.Contains(spec.Surfaces, "menubar") && spec.Input == nil {
					want = append(want, menuTitle(spec, st))
				}
			}
			if got := titles(it.Children); !slices.Equal(got, want) {
				t.Fatalf("submenu %q\nwant    %q", got, want)
			}
			for _, st := range row.Actions {
				spec := specOf(t, st.ID)
				if !slices.Contains(spec.Surfaces, "menubar") || spec.Input != nil {
					continue
				}
				child, _ := find(it.Children, menuTitle(spec, st))
				want := strings.Join(slices.DeleteFunc([]string{spec.Summary, map[bool]string{true: st.Note, false: st.Reason}[st.Enabled]}, func(s string) bool { return s == "" }), "\n")
				if child.Tooltip != want || want == "" {
					t.Errorf("%s: tooltip %q, want %q", st.ID, child.Tooltip, want)
				}
			}
			for _, st := range row.Actions {
				spec := specOf(t, st.ID)
				if !slices.Contains(spec.Surfaces, "menubar") || spec.Input != nil || !st.Enabled {
					continue
				}
				child, _ := find(it.Children, menuTitle(spec, st))
				do := child.Action
				if st.Confirm != "" {
					if child.Action != nil || len(child.Children) != 1 || child.Children[0].Title != st.Confirm {
						t.Fatalf("%s confirms with %+v", st.ID, child)
					}
					do = child.Children[0].Action
				}
				do()
				waitFor(t, func() bool {
					for _, q := range r.d.snapshotRequests() {
						if q.name == st.ID && q.req.Basis == st.Basis && q.req.Source == "menubar" {
							return true
						}
					}
					return false
				})
			}
		})
	}
}

func TestSpecsAreReadAgainOnlyWhenTheVersionChanges(t *testing.T) {
	r := newRig(t)
	row := goldenRows(t)["pending"]
	row.SpecsVersion = goldenSpecs().Version
	r.d.rows["loop"] = []api.Row{row}
	r.c.Refresh(context.Background())
	r.c.Refresh(context.Background())
	if r.d.specReads != 1 {
		t.Fatalf("%d reads", r.d.specReads)
	}
	row.SpecsVersion = "next"
	r.d.rows["loop"] = []api.Row{row}
	r.c.Refresh(context.Background())
	if r.d.specReads != 2 {
		t.Fatalf("%d reads after a new version", r.d.specReads)
	}
}

func TestBriefingLine(t *testing.T) {
	r := newRig(t)
	waiting := reviewRow(1)
	waiting.Summary.Line = "briefing… (attempt 2; rate limited)"
	session := reviewRow(3)
	session.Summary.Line = "briefing…"
	golden := goldenRows(t)
	waiting.Actions = golden["intake_waiting_cli"].Actions
	session.Actions = golden["intake_waiting_session"].Actions
	starting := reviewRow(2)
	starting.Actions = golden["tracked"].Actions
	r.d.rows["loop"] = []api.Row{waiting, starting, session}
	r.c.Refresh(context.Background())
	it, ok := find(r.tray.menu, "acme/api#1")
	if !ok || it.Title != "acme/api#1 · briefing… (attempt 2; rate limited)" {
		t.Fatalf("menu %q", titles(r.tray.menu))
	}
	if other, _ := find(r.tray.menu, "acme/api#2"); other.Title != "acme/api#2 · review requested" || slices.Contains(titles(other.Children), "Cancel") {
		t.Fatalf("a starting intake: %q %q", other.Title, titles(other.Children))
	}
	if s, _ := find(r.tray.menu, "acme/api#3"); slices.Contains(titles(s.Children), "Start without brief") || !slices.Contains(titles(s.Children), "Cancel") {
		t.Fatalf("a turn by hand offers %q", titles(s.Children))
	}
	without, _ := find(it.Children, "Start without brief")
	cancel, _ := find(it.Children, "Cancel")
	without.Action()
	cancel.Action()
	waitFor(t, func() bool {
		a := r.d.snapshotActions()
		return slices.Contains(a, "intake_without acme/api#1") && slices.Contains(a, "intake_cancel acme/api#1")
	})
}

func TestBreakerHeader(t *testing.T) {
	r := newRig(t)
	since := time.Date(2026, 10, 14, 13, 40, 0, 0, time.Local)
	r.d.health = api.Health{Briefs: &api.BriefsHealth{Open: true, Since: since, Error: "claude is rate limited", Next: since.Add(15 * time.Minute)}}
	r.c.Refresh(context.Background())
	if got := titles(r.tray.menu); got[0] != "briefs unavailable since 13:40: claude is rate limited" || got[1] != "---" {
		t.Fatalf("menu %q", got)
	}
	r.d.health = api.Health{Briefs: &api.BriefsHealth{}}
	r.c.Refresh(context.Background())
	if got := titles(r.tray.menu); strings.HasPrefix(got[0], "briefs unavailable") {
		t.Fatalf("a closed breaker shows: %q", got)
	}
}

func TestUnavailableBriefsHeader(t *testing.T) {
	r := newRig(t)
	r.d.health = api.Health{Briefs: &api.BriefsHealth{Unavailable: "agent \"plain\" cannot answer headless calls"}}
	r.c.Refresh(context.Background())
	if got := titles(r.tray.menu); got[0] != "briefs unavailable: agent \"plain\" cannot answer headless calls" || got[1] != "---" {
		t.Fatalf("menu %q", got)
	}
}

func TestPauseItemTogglesAndQuitQuits(t *testing.T) {
	r := newRig(t)
	r.d.health.Paused = true
	r.c.Refresh(context.Background())
	it, ok := find(r.tray.menu, "Resume automatic rounds")
	if !ok {
		t.Fatalf("menu %q", titles(r.tray.menu))
	}
	it.Action()
	waitFor(t, func() bool { r.d.mu.Lock(); defer r.d.mu.Unlock(); return slices.Equal(r.d.paused, []bool{false}) })
	q, _ := find(r.tray.menu, "Quit "+brand.Name)
	q.Action()
	if !r.quit {
		t.Fatal("quit not called")
	}
}

func TestMenuShowsBeforeThePermissionIsAnswered(t *testing.T) {
	r := newRig(t)
	r.notes.block = make(chan struct{})
	r.d.rows["loop"] = []api.Row{reviewRow(2)}
	r.d.counters = api.Counters{Badge: 1}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- r.c.Run(ctx) }()
	waitFor(t, func() bool { return r.title() == "PR 1" })
	close(r.notes.block)
	cancel()
	<-done
}

func TestTheAppIsReportedBeforeThePermissionIsAnswered(t *testing.T) {
	r := newRig(t)
	r.notes.perm = PermissionNotDetermined
	r.notes.block = make(chan struct{})
	defer close(r.notes.block)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- r.c.Run(ctx) }()
	waitFor(t, func() bool {
		r.d.mu.Lock()
		defer r.d.mu.Unlock()
		return len(r.d.app) > 0
	})
	cancel()
	<-done
	r.d.mu.Lock()
	defer r.d.mu.Unlock()
	if st := r.d.app[0]; st.State != "running" || st.Notifications != "not-determined" {
		t.Fatalf("%+v", st)
	}
}

func TestPermissionChangesAreReported(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	r.c.checkPermission(ctx)
	r.c.checkPermission(ctx)
	r.notes.setPerm(PermissionDenied)
	r.c.checkPermission(ctx)
	r.d.mu.Lock()
	defer r.d.mu.Unlock()
	if len(r.d.app) != 2 || r.d.app[1].Notifications != "denied" {
		t.Fatalf("%+v", r.d.app)
	}
}

func TestRunReportsThePermission(t *testing.T) {
	r := newRig(t)
	r.notes.perm = PermissionDenied
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- r.c.Run(ctx) }()
	waitFor(t, func() bool { r.d.mu.Lock(); defer r.d.mu.Unlock(); return len(r.d.app) > 0 })
	cancel()
	<-done
	r.d.mu.Lock()
	defer r.d.mu.Unlock()
	if st := r.d.app[0]; st.State != "running" || st.Notifications != "denied" {
		t.Fatalf("%+v", st)
	}
}

func TestAStreamThatEndsAtOnceDoesNotSpin(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t)
		r.d.dropped = true
		stop := run(t, r)
		time.Sleep(refreshMin * 5 / 2)
		synctest.Wait()
		stop()
		r.d.mu.Lock()
		defer r.d.mu.Unlock()
		if r.d.reads != 3 {
			t.Fatalf("%d reads of the rows in 2.5 refresh intervals, want 3", r.d.reads)
		}
	})
}

func (f *fakeDaemon) snapshotRequests() []actionCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.reqs)
}

func (f *fakeDaemon) snapshotActions() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.actions)
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(time.Minute)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestRowOpensInTheDaemonsWorkspaceManager(t *testing.T) {
	for manager, want := range map[string]string{"Orca": "Open in Orca", "": "Open workspace", "Claude Desktop,Orca": "Open workspace"} {
		r := newRig(t)
		row := goldenRows(t)["tracked"]
		row.Unit, row.Label = uref(1), uref(1).Key
		r.d.rows["loop"] = []api.Row{row}
		var managers []string
		if manager != "" {
			managers = strings.Split(manager, ",")
		}
		r.d.health = api.Health{WorkspaceManagers: managers}
		r.c.Refresh(context.Background())
		it, _ := find(r.tray.menu, "acme/api#1")
		if got := titles(it.Children); got[len(got)-1] != want {
			t.Errorf("manager %q: submenu %q, want %q last", manager, got, want)
		}
	}
}

func goldenFile(name string, into any) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "daemon", "testdata", "wire", name))
	if err == nil {
		err = json.Unmarshal([]byte(brandtest.Expand(string(raw))), into)
	}
	if err != nil {
		panic(err)
	}
}

func goldenSpecs() api.ActionsResponse {
	var res api.ActionsResponse
	goldenFile("specs.json", &res)
	return res
}

func goldenRows(t testing.TB) map[string]api.Row {
	t.Helper()
	var rows map[string]api.Row
	goldenFile("actions.json", &rows)
	return rows
}

func specOf(t testing.TB, id string) api.ActionSpec {
	t.Helper()
	for _, s := range goldenSpecs().Specs {
		if s.ID == id {
			return s
		}
	}
	t.Fatalf("no spec %q", id)
	return api.ActionSpec{}
}

func menuTitle(spec api.ActionSpec, st api.ActionState) string {
	title := spec.Label
	if st.Label != "" {
		title = st.Label
	}
	switch {
	case spec.ID == "open":
		title = "Open workspace"
	case !st.Enabled:
		title += " (" + st.Reason + ")"
	case st.Confirm != "":
		title += "…"
	}
	return title
}

type fakeClock struct {
	mu     sync.Mutex
	now    time.Time
	timers []fakeTimer
	waits  []time.Duration
}

type fakeTimer struct {
	at time.Time
	ch chan time.Time
}

func (k *fakeClock) Now() time.Time { k.mu.Lock(); defer k.mu.Unlock(); return k.now }

func (k *fakeClock) After(d time.Duration) <-chan time.Time {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.waits = append(k.waits, d)
	ch := make(chan time.Time, 1)
	k.timers = append(k.timers, fakeTimer{k.now.Add(d), ch})
	return ch
}

func (k *fakeClock) advance(d time.Duration) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.now = k.now.Add(d)
	keep := k.timers[:0]
	for _, t := range k.timers {
		if t.at.After(k.now) {
			keep = append(keep, t)
			continue
		}
		t.ch <- k.now
	}
	k.timers = keep
}

func (k *fakeClock) waited() []time.Duration {
	k.mu.Lock()
	defer k.mu.Unlock()
	return slices.Clone(k.waits)
}

func (f *fakeDaemon) counts() (rows, health int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.reads, f.healthN
}

func clockedRig(t *testing.T) (*rig, *fakeClock) {
	t.Helper()
	r := newRig(t)
	k := &fakeClock{now: now}
	r.c.cfg.Now, r.c.cfg.After = k.Now, k.After
	return r, k
}

func run(t *testing.T, r *rig) (stop func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- r.c.Run(ctx) }()
	return func() { cancel(); <-done }
}

func TestMenubarOneRequestPerChange(t *testing.T) {
	r, _ := clockedRig(t)
	r.d.events = make(chan api.Change)
	r.d.rows["loop"] = []api.Row{reviewRow(2)}
	stop := run(t, r)
	defer stop()
	waitFor(t, func() bool { rows, _ := r.d.counts(); return rows == 1 })
	waitFor(t, func() bool { r.d.mu.Lock(); defer r.d.mu.Unlock(); return r.d.eventsN == 1 })
	moved := reviewRow(2)
	moved.Title = "moved"
	r.d.mu.Lock()
	r.d.delta = &api.RowsPage{Epoch: "e1", Rev: 7, Rows: []api.Row{moved}, Removed: []unit.Unit{}}
	r.d.mu.Unlock()
	r.d.events <- api.Change{Type: "changed", Epoch: "e1", Rev: 7, Units: []unit.Unit{moved.Unit}}
	waitFor(t, func() bool { rows, _ := r.d.counts(); return rows == 2 })
	rows, health := r.d.counts()
	if rows != 2 || health != 1 {
		t.Fatalf("%d rows requests and %d health requests after one change, want 2 and 1 (the boot load and the change)", rows, health)
	}
	r.d.mu.Lock()
	defer r.d.mu.Unlock()
	if want := (api.Since{Epoch: "e1", Rev: 1}); r.d.sinces[1] != want {
		t.Fatalf("the change pulled since %+v, want %+v", r.d.sinces[1], want)
	}
}

func TestMenubarHealthOnlyWhenTheChangeNamesIt(t *testing.T) {
	r, _ := clockedRig(t)
	r.d.events = make(chan api.Change)
	stop := run(t, r)
	defer stop()
	waitFor(t, func() bool { r.d.mu.Lock(); defer r.d.mu.Unlock(); return r.d.eventsN == 1 })
	r.d.events <- api.Change{Type: "changed", Health: true}
	waitFor(t, func() bool { _, health := r.d.counts(); return health == 2 })
}

func TestMenubarNoTickWhileConnected(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r, k := clockedRig(t)
		r.d.events = make(chan api.Change)
		stop := run(t, r)
		defer stop()
		synctest.Wait()
		rows, health := r.d.counts()
		for range 20 {
			k.advance(30 * time.Second)
		}
		synctest.Wait()
		if r2, h2 := r.d.counts(); r2 != rows || h2 != health {
			t.Fatalf("requests grew from %d/%d to %d/%d in 10 minutes with the stream up", rows, health, r2, h2)
		}
		if w := k.waited(); len(w) != 0 {
			t.Fatalf("timers set while connected: %v", w)
		}
	})
}

func TestMenubarPollsWhileDisconnected(t *testing.T) {
	r, k := clockedRig(t)
	r.d.eventsErr = errors.New("stream refused")
	stop := run(t, r)
	defer stop()
	for i := 1; i <= 3; i++ {
		waitFor(t, func() bool { r.d.mu.Lock(); defer r.d.mu.Unlock(); return r.d.eventsN == i && len(k.waited()) >= i })
		if rows, _ := r.d.counts(); rows != i {
			t.Fatalf("%d pulls before tick %d", rows, i)
		}
		k.advance(refreshMax)
	}
}

func healthOnlyRig(t *testing.T) (*rig, func()) {
	t.Helper()
	r, _ := clockedRig(t)
	r.d.events = make(chan api.Change)
	r.d.rows["loop"] = []api.Row{reviewRow(2)}
	stop := run(t, r)
	waitFor(t, func() bool { r.d.mu.Lock(); defer r.d.mu.Unlock(); return r.d.eventsN == 1 })
	return r, stop
}

func TestMenubarHealthOnlyChangeSkipsTheRowsPull(t *testing.T) {
	r, stop := healthOnlyRig(t)
	defer stop()
	r.d.events <- api.Change{Type: "changed", Epoch: "e1", Rev: 1, Health: true}
	waitFor(t, func() bool { _, health := r.d.counts(); return health == 2 })
	if rows, _ := r.d.counts(); rows != 1 {
		t.Fatalf("%d rows pulls, want the boot one only", rows)
	}
}

func TestMenubarRefusedEpochPullsFromZero(t *testing.T) {
	r := newRig(t)
	r.d.rows["loop"] = []api.Row{reviewRow(2)}
	r.c.Refresh(context.Background())
	r.d.mu.Lock()
	r.d.delta = &api.RowsPage{Epoch: "e2", Rev: 1, Rows: []api.Row{reviewRow(3)}, Removed: []unit.Unit{}}
	r.d.rows["loop"] = []api.Row{reviewRow(4), reviewRow(5)}
	r.d.mu.Unlock()
	r.c.Refresh(context.Background())
	r.d.mu.Lock()
	defer r.d.mu.Unlock()
	if n := len(r.d.sinces); n != 3 || r.d.sinces[2] != (api.Since{}) {
		t.Fatalf("requests since %+v, want a third from zero", r.d.sinces)
	}
	if _, ok := find(r.tray.menu, "In loop (2)"); !ok {
		t.Fatalf("menu %q, want the rows of the full pull", titles(r.tray.menu))
	}
}

func (f *fakeNotifier) postedIDs() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, n := range f.posted {
		out = append(out, n.ID)
	}
	return out
}

func TestMenuLabelIsTheSummaryLine(t *testing.T) {
	r := newRig(t)
	rows := []api.Row{reviewRow(1), reviewRow(2), reviewRow(3)}
	rows[0].Summary.Line = "briefing… (attempt 3)"
	rows[1].Kind, rows[1].Summary.Line = "round_pending", "round soon"
	rows[2].Kind, rows[2].Summary.Line = "loop_working", "agent working"
	r.d.rows["loop"] = rows
	r.c.Refresh(context.Background())
	for label, want := range map[string]string{"acme/api#1": "briefing… (attempt 3)", "acme/api#2": "round soon", "acme/api#3": "agent working"} {
		if it, ok := find(r.tray.menu, label); !ok || it.Title != label+" · "+want {
			t.Errorf("%s: %q", label, it.Title)
		}
	}
}

func TestMenuListsAThreadRow(t *testing.T) {
	r := newRig(t)
	thread := api.Row{Unit: unit.Unit{Provider: "thread", Key: "C1/171.1"}, Link: "https://chat.example/C1/171.1", Label: "#general", Title: "Deploy window?",
		Kind: "waiting", Views: []string{api.ViewLoop}, Summary: api.Summary{WaitsOn: "me", Since: now, Reason: "ana asked you", Line: "ana asked you"}}
	r.d.rows["loop"] = []api.Row{thread}
	r.c.Refresh(context.Background())
	it, ok := find(r.tray.menu, "#general")
	if !ok || it.Title != "#general · ana asked you" {
		t.Fatalf("menu %q", titles(r.tray.menu))
	}
	it.Action()
	waitFor(t, func() bool { o := r.openedURLs(); return len(o) == 1 && o[0] == thread.Link })
}

func TestARowOpensInTheCockpitOnlyWhenItsPluginServesADetail(t *testing.T) {
	r := newRig(t)
	row := reviewRow(1)
	row.Link = "https://forge.example/acme/api/1"
	r.d.rows["loop"] = []api.Row{row}
	r.c.Refresh(context.Background())
	it, ok := find(r.tray.menu, row.Label)
	if !ok {
		t.Fatalf("menu %q", titles(r.tray.menu))
	}
	it.Action()
	waitFor(t, func() bool { o := r.openedURLs(); return len(o) == 1 && strings.HasPrefix(o[0], "cockpit#") })
}
