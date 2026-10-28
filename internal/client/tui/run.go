package tui

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/api/client"
	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/client/rowset"
	"github.com/wloophq/wloop/internal/domain/unit"
)

type Daemon interface {
	RowsSince(ctx context.Context, since api.Since) (api.RowsPage, error)
	Health(ctx context.Context) (api.Health, error)
	Catalog(ctx context.Context) (api.CatalogResponse, error)
	Plugins(ctx context.Context) ([]api.Plugin, error)
	Actions(ctx context.Context) (api.ActionsResponse, error)
	Ask(ctx context.Context, u unit.Unit, action string, req api.ActionRequest) (api.ActionResult, error)
	Events(ctx context.Context, client string, since api.Since) (<-chan api.Change, error)
	Plan(ctx context.Context, q api.PlanQuery) (api.PlanResponse, error)
	ActOnPlan(ctx context.Context, req api.PlanActRequest) (api.PlanResponse, error)
	Views(ctx context.Context) (api.ViewsCatalog, error)
	TasksSince(ctx context.Context, q client.TaskQuery, since api.Since) (api.TasksPage, error)
	Counters(ctx context.Context, area string, terms ...string) (api.Counters, error)
	Task(ctx context.Context, id string) (api.TaskView, error)
	TaskDo(ctx context.Context, id, verb string, req api.TaskRequest) (api.TaskResult, error)
	TaskDrafts(ctx context.Context, task string) ([]api.Draft, error)
	CreateTask(ctx context.Context, req api.TaskCreate) (api.TaskView, error)
	Keys(ctx context.Context, surface string) (api.KeysResponse, error)
	Review(ctx context.Context) (api.Review, error)
	ReviewStepDone(ctx context.Context, step string) (api.Review, error)
}

type Terminal interface {
	Size() (width, height int)
	Draw(lines []string) error
	Keys() <-chan Key
}

// A Suspender can hand the terminal back to the shell on Ctrl-Z.
type Suspender interface {
	Suspend() error
}

// An Editor can hand the terminal to the user's editor on a text and return
// the text it left.
type Editor interface {
	Edit(text string) (string, error)
}

type Config struct {
	Daemon Daemon
	Term   Terminal
	Now    func() time.Time
	// Tick redraws the ages and countdowns; zero means every second.
	Tick time.Duration
	// Refresh polls the rows while the daemon has no event stream; zero
	// means every 30 seconds. With a stream up, nothing polls.
	Refresh time.Duration
	// Reconnect is the wait before asking for the event stream again while
	// the daemon does not answer; zero means 2 seconds.
	Reconnect time.Duration
	// Color styles the screen; the caller decides it as termfmt does.
	Color bool
	// OpenURL opens a unit's link, as the open command does; nil cannot.
	OpenURL func(url string) error
}

type loaded struct {
	rows   []api.Row
	health api.Health
	specs  *api.ActionsResponse
	// catalog and plugins are the served catalog and plugins when this
	// load read them.
	catalog *api.CatalogResponse
	plugins []api.Plugin
	// specsErr is a failed read of the specs: the rows are still fresh.
	specsErr error
	err      error
	// plan is the stored plan when this load read it; planErr is why it
	// could not, which leaves the rows fresh.
	plan    *[]api.PlanItem
	closed  []api.ClosedSession
	planErr error
	// viewsCat and keys are the served views and bindings when this load read
	// them; tasks is the page of the task view on screen when it pulled
	// one, counters the counts a change carried.
	viewsCat *api.ViewsCatalog
	keys     *api.KeysResponse
	tasks    *tasksRead
	tasksErr error
	counters *api.Counters
	// filtered are the counts of the tasks the filter keeps, when this
	// load read them.
	filtered    *filteredRead
	filteredErr error
	// review is the Review when the screen shows it.
	review    *api.Review
	reviewErr error
}

type tasksRead struct {
	view     string
	tasks    []api.TaskView
	counters api.Counters
}

// filteredRead are the counters of area narrowed to the tasks the
// filter keeps.
type filteredRead struct {
	area     string
	counters api.Counters
}

type finished struct {
	cmd Command
	carried
	err error
}

// carried is what a command brought back: its lines, the task it left, and
// for a detail the drafts made for its task.
type carried struct {
	lines  []string
	task   api.TaskView
	drafts []api.Draft
}

// Run draws the views and applies keys until q, Ctrl-C, the end of the keys
// or ctx. An action sent to the daemon is not cancelled by quitting: q
// waits for the running ones, and a second q leaves without them.
func Run(ctx context.Context, cfg Config) error {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Tick <= 0 {
		cfg.Tick = time.Second
	}
	if cfg.Refresh <= 0 {
		cfg.Refresh = 30 * time.Second
	}
	if cfg.Reconnect <= 0 {
		cfg.Reconnect = 2 * time.Second
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	m := NewModel()
	m.SetColor(cfg.Color)
	refresh := make(chan struct{}, 1)
	kick := func() {
		select {
		case refresh <- struct{}{}:
		default:
		}
	}
	results := make(chan loaded)
	done := make(chan finished)
	crashed := make(chan error, 1)
	var answering atomic.Bool
	var at cursor
	var due wants
	var shown query
	guard(crashed, func() { follow(ctx, cfg, &answering, &at, &due, kick) })
	guard(crashed, func() { load(ctx, cfg.Daemon, refresh, results, &answering, &at, &due, &shown) })
	tick := time.NewTicker(cfg.Tick)
	defer tick.Stop()
	keys := cfg.Term.Keys()
	running, leaving := 0, false
	start := func(c Command) {
		running++
		guard(crashed, func() {
			got, err := carry(context.WithoutCancel(ctx), cfg, c)
			select {
			case done <- finished{c, got, err}:
			case <-ctx.Done():
			}
		})
	}
	// track pulls the tasks of the task view on screen once it changed,
	// and asks again for an open detail a page changed.
	track := func() {
		view, area, ok := m.TaskQuery()
		if (shown.set(view, area, ok) && ok) || shown.setReview(m.inReview()) || shown.setFilter(m.area, m.FilterTerms()) {
			kick()
		}
		if c, ok := m.Refetch(); ok {
			start(c)
		}
	}
	for {
		w, h := cfg.Term.Size()
		// One cell short of the edge: a full last line makes some terminals scroll.
		if err := cfg.Term.Draw(m.Render(w-1, h, cfg.Now())); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return nil
		case err := <-crashed:
			return err
		case k, ok := <-keys:
			if !ok {
				return nil
			}
			if k.Name == KeyCtrlZ {
				if err := suspend(cfg.Term, m); err != nil {
					return err
				}
				continue
			}
			if c, send := m.Key(k); send {
				start(c)
			}
			if e := m.TakeEdit(); e != nil {
				text, err := edit(cfg.Term, e.Text)
				if c, send := m.Edited(e, text, err); send {
					start(c)
				}
			}
			track()
			if m.Quit() {
				if running == 0 || leaving {
					return nil
				}
				leaving = true
				m.Stay(waitingFor(running))
			}
		case r := <-results:
			if r.err != nil {
				m.SetError(r.err)
			} else {
				m.apply(r)
			}
			track()
		case f := <-done:
			running--
			if f.cmd.fetch {
				m.SetDetail(f.cmd.task, f.task, f.err)
				m.SetDrafts(f.cmd.task, f.drafts)
			} else {
				m.Done(f.cmd, f.lines, f.err)
			}
			if leaving {
				if running == 0 {
					return nil
				}
				m.Stay(waitingFor(running))
			}
			if !f.cmd.fetch || f.cmd.seen {
				due.rows.Store(true)
				due.plan.Store(true)
				due.tasks.Store(true)
				due.filtered.Store(true)
				kick()
			}
		case <-tick.C:
		}
	}
}

// apply takes what a load read.
func (m *Model) apply(r loaded) {
	if r.catalog != nil {
		m.SetCatalog(*r.catalog)
	}
	if r.plugins != nil {
		m.SetPlugins(r.plugins)
	}
	if r.viewsCat != nil {
		m.SetViews(*r.viewsCat)
	}
	if r.keys != nil {
		m.SetKeys(r.keys.Bindings)
	}
	m.SetData(r.rows, r.health)
	if r.counters != nil {
		m.SetCounters(*r.counters)
	}
	if r.review != nil {
		m.SetReview(*r.review)
	}
	if e := r.tasks; e != nil {
		m.SetTasks(e.view, e.tasks, e.counters)
	}
	if f := r.filtered; f != nil {
		m.SetFilteredCounters(f.area, f.counters)
	}
	if r.plan != nil || r.planErr != nil {
		m.SetPlan(deref(r.plan), r.planErr)
		if r.plan != nil {
			m.SetClosed(r.closed)
		}
	}
	if r.specs != nil {
		m.SetSpecs(r.specs.Specs)
		m.SetPlanSpecs(r.specs.Plan)
	}
	switch {
	case r.specsErr != nil:
		m.status = "actions not refreshed: " + r.specsErr.Error()
	case r.tasksErr != nil:
		m.status = "tasks not refreshed: " + r.tasksErr.Error()
	case r.reviewErr != nil:
		m.status = "review not refreshed: " + r.reviewErr.Error()
	case r.filteredErr != nil:
		m.status = "filtered counts not refreshed: " + r.filteredErr.Error()
	}
}

// edit runs the terminal's editor on text, when it has one.
func edit(t Terminal, text string) (string, error) {
	ed, ok := t.(Editor)
	if !ok {
		return "", errors.New("no editor here: type the text on the bottom line")
	}
	return ed.Edit(text)
}

// guard runs f in a goroutine and turns a panic into an error for Run, so
// the terminal is restored by Run's caller.
func guard(crashed chan<- error, f func()) {
	go func() {
		defer func() {
			if p := recover(); p != nil {
				select {
				case crashed <- fmt.Errorf(brand.Command+" tui: %v", p):
				default:
				}
			}
		}()
		f()
	}()
}

func waitingFor(running int) string {
	return fmt.Sprintf("waiting for %d running action(s) to finish; q again leaves without them", running)
}

// suspend hands the terminal back on Ctrl-Z. A failure is fatal: the
// terminal may be neither raw nor restored, and Run's caller restores it.
func suspend(t Terminal, m *Model) error {
	s, ok := t.(Suspender)
	if !ok {
		m.status = "suspend is not supported here"
		return nil
	}
	if err := s.Suspend(); err != nil {
		return fmt.Errorf("suspend: %w", err)
	}
	return nil
}

// cursor is where the loaded rows stand, shared with the event stream so it
// resumes from there.
type cursor struct {
	mu sync.Mutex
	at api.Since
}

func (c *cursor) get() api.Since {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.at
}

func (c *cursor) set(s api.Since) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.at = s
}

// wants are what the next load asks the daemon for; counters are the
// counts the last change carried.
type wants struct {
	health, rows, plan, tasks, filtered atomic.Bool
	counters                            atomic.Pointer[api.Counters]
}

func (r *wants) all() {
	r.health.Store(true)
	r.rows.Store(true)
	r.plan.Store(true)
	r.tasks.Store(true)
	r.filtered.Store(true)
}

// query is the task view the screen shows and the area it follows, shared
// with the loads.
type query struct {
	mu         sync.Mutex
	view, area string
	ok         bool
	review     bool
	// filterArea and terms are the area the tabs count and the filter's
	// words, which narrow the counts.
	filterArea string
	terms      []string
}

// setFilter records the filter's words and the area the tabs count,
// reporting a change while the filter has words.
func (q *query) setFilter(area string, terms []string) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	changed := q.filterArea != area || !slices.Equal(q.terms, terms)
	q.filterArea, q.terms = area, terms
	return changed && len(terms) > 0
}

func (q *query) filter() (string, []string) {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.filterArea, q.terms
}

// setReview records whether the screen shows the Review, reporting a change to showing it.
func (q *query) setReview(on bool) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	changed := on && !q.review
	q.review = on
	return changed
}

func (q *query) reviewing() bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.review
}

// set reports whether the query changed.
func (q *query) set(view, area string, ok bool) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	changed := q.view != view || q.area != area || q.ok != ok
	q.view, q.area, q.ok = view, area, ok
	return changed
}

func (q *query) get() (client.TaskQuery, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	return client.TaskQuery{View: q.view, Area: q.area}, q.ok
}

// pull brings the set up to the daemon: a delta from where it stands, or
// every row when the daemon cannot give one.
func pull(ctx context.Context, d Daemon, set *rowset.Set) error {
	page, err := d.RowsSince(ctx, set.Since())
	if err != nil {
		return err
	}
	if set.Apply(page) {
		return nil
	}
	if page, err = d.RowsSince(ctx, api.Since{}); err != nil {
		return err
	}
	set.Apply(page)
	return nil
}

// pullTasks brings the tasks of q up to the daemon as pull does rows.
func pullTasks(ctx context.Context, d Daemon, q client.TaskQuery, set *taskSet) (api.Counters, error) {
	page, err := d.TasksSince(ctx, q, set.since)
	if err != nil {
		return api.Counters{}, err
	}
	if set.apply(page) {
		return page.Counters, nil
	}
	if page, err = d.TasksSince(ctx, q, api.Since{}); err != nil {
		return api.Counters{}, err
	}
	set.apply(page)
	return page.Counters, nil
}

// onceReads are the reads a load makes once, and again only after a failure.
type onceReads struct {
	catalog, plugins, viewsCat, keys bool
	specs                            string
}

func load(ctx context.Context, d Daemon, refresh <-chan struct{}, out chan<- loaded, answering *atomic.Bool, at *cursor, due *wants, shown *query) {
	var have onceReads
	var set rowset.Set
	var h api.Health
	needHealth, needRows, needPlan := true, true, true
	var plan []api.PlanItem
	var err error
	sets := map[string]*taskSet{}
	var pulled, counted string
	for {
		select {
		case <-ctx.Done():
			return
		case <-refresh:
		}
		needRows = due.rows.Swap(false) || needRows
		err = nil
		if needRows {
			err = pull(ctx, d, &set)
			at.set(set.Since())
			needRows = err != nil
		}
		needHealth = due.health.Swap(false) || needHealth
		if err == nil && needHealth {
			h, err = d.Health(ctx)
		}
		needHealth = err != nil
		var planRead *[]api.PlanItem
		var closed []api.ClosedSession
		var planErr error
		needPlan = due.plan.Swap(false) || needPlan
		if err == nil && needPlan {
			var res api.PlanResponse
			if res, planErr = d.Plan(ctx, api.PlanQuery{Closed: true}); planErr == nil {
				plan, closed = res.Items, res.Closed
				read := slices.Clone(plan)
				planRead = &read
			}
			needPlan = planErr != nil
		}
		rows := set.View(api.ViewAll)
		r := loaded{rows: rows, health: h, err: err, plan: planRead, closed: closed, planErr: planErr}
		if err == nil {
			r.specsErr = have.read(ctx, d, rows, len(plan) > 0, &r)
			if shown.reviewing() {
				if rv, e := d.Review(ctx); e != nil {
					r.reviewErr = e
				} else {
					r.review = &rv
				}
			}
			r.tasks, r.tasksErr = readTasks(ctx, d, have.viewsCat, due, shown, sets, &pulled)
			r.counters = due.counters.Swap(nil)
			r.filtered, r.filteredErr = readFiltered(ctx, d, have.viewsCat, due, shown, &counted)
		}
		answering.Store(err == nil)
		select {
		case out <- r:
		case <-ctx.Done():
			return
		}
	}
}

// read fills r with the specs when their version changed, the bindings
// with them, and the catalog, the plugins and the task views until each
// was read once; the error is the first failure.
func (have *onceReads) read(ctx context.Context, d Daemon, rows []api.Row, planned bool, r *loaded) error {
	var failed error
	if want := specsVersion(rows); (want != "" && want != have.specs) || (have.specs == "" && planned) {
		if res, e := d.Actions(ctx); e != nil {
			failed = e
		} else {
			r.specs, have.specs, have.keys = &res, res.Version, false
		}
	}
	if !have.keys {
		if res, e := d.Keys(ctx, surface); e != nil {
			failed = cmp.Or(failed, e)
		} else {
			r.keys, have.keys = &res, true
		}
	}
	if !have.catalog {
		if res, e := d.Catalog(ctx); e != nil {
			failed = cmp.Or(failed, e)
		} else {
			r.catalog, have.catalog = &res, true
		}
	}
	if !have.plugins {
		if res, e := d.Plugins(ctx); e != nil {
			failed = cmp.Or(failed, e)
		} else {
			r.plugins, have.plugins = nonNilPlugins(res), true
		}
	}
	if !have.viewsCat {
		if res, e := d.Views(ctx); e == nil {
			r.viewsCat, have.viewsCat = &res, true
		}
	}
	return failed
}

func nonNilPlugins(ps []api.Plugin) []api.Plugin {
	if ps == nil {
		return []api.Plugin{}
	}
	return ps
}

// readTasks pulls the page of the task view on screen when a change named
// tasks or the screen moved to another view or area since the last pull.
func readTasks(ctx context.Context, d Daemon, onViews bool, due *wants, shown *query, sets map[string]*taskSet, pulled *string) (*tasksRead, error) {
	q, ok := shown.get()
	named := due.tasks.Swap(false)
	if !onViews || !ok {
		if named {
			due.tasks.Store(true)
		}
		return nil, nil
	}
	key := q.View + "\x00" + q.Area
	if !named && key == *pulled {
		return nil, nil
	}
	set := sets[key]
	if set == nil {
		set = &taskSet{}
		sets[key] = set
	}
	c, err := pullTasks(ctx, d, q, set)
	if err != nil {
		due.tasks.Store(true)
		return nil, err
	}
	*pulled = key
	return &tasksRead{view: q.View, tasks: set.list(), counters: c}, nil
}

// readFiltered reads the counters the filter narrows when the filter has
// words and they, the area or the tasks changed since the last read.
func readFiltered(ctx context.Context, d Daemon, onViews bool, due *wants, shown *query, counted *string) (*filteredRead, error) {
	area, terms := shown.filter()
	named := due.filtered.Swap(false)
	if !onViews || len(terms) == 0 {
		*counted = ""
		return nil, nil
	}
	key := area + "\x00" + strings.Join(terms, "\x00")
	if !named && key == *counted {
		return nil, nil
	}
	c, err := d.Counters(ctx, area, terms...)
	if err != nil {
		due.filtered.Store(true)
		return nil, err
	}
	*counted = key
	return &filteredRead{area: area, counters: c}, nil
}

// follow asks for a refresh on every change the daemon announces and on each
// reconnection. A daemon that answers but has no event stream is polled
// every Refresh instead of being asked for it again every Reconnect.
func follow(ctx context.Context, cfg Config, answering *atomic.Bool, at *cursor, due *wants, kick func()) {
	for {
		events, err := cfg.Daemon.Events(ctx, "tui", at.get())
		due.all()
		kick()
		wait := cfg.Reconnect
		if err == nil {
			listen(ctx, events, at, due, kick)
		} else if answering.Load() {
			wait = cfg.Refresh
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
	}
}

// listen asks for the rows unless a change only names the health and is not
// ahead of the loaded rows, and for the health when a change names it.
func listen(ctx context.Context, events <-chan api.Change, at *cursor, due *wants, kick func()) {
	for {
		select {
		case <-ctx.Done():
			return
		case c, ok := <-events:
			if !ok {
				return
			}
			held := at.get()
			if !(c.Health && !c.All && len(c.Units) == 0 && c.Epoch == held.Epoch && c.Rev <= held.Rev) {
				due.rows.Store(true)
			}
			if c.Health || c.All {
				due.health.Store(true)
			}
			if c.Counters != nil || c.All {
				due.tasks.Store(true)
				due.filtered.Store(true)
			}
			if c.Counters != nil {
				counters := *c.Counters
				due.counters.Store(&counters)
			}
			due.plan.Store(true)
			kick()
		}
	}
}

func specsVersion(rows []api.Row) string {
	for _, r := range rows {
		if r.SpecsVersion != "" {
			return r.SpecsVersion
		}
	}
	return ""
}

func acceptedLine(i api.Intake) string {
	switch {
	case !i.Joined:
		return fmt.Sprintf("accepted as intake %d", i.Seq)
	case i.Waiting():
		return fmt.Sprintf("joined intake %d", i.Seq)
	}
	return fmt.Sprintf("intake %d is already starting the turn; this request changed nothing", i.Seq)
}

// carry takes c to the daemon: a task's verb, detail or capture, a link
// to open, a plan action on its item, any other on its unit. It returns
// the lines that report it, and the task a detail read.
func carry(ctx context.Context, cfg Config, c Command) (carried, error) {
	d := cfg.Daemon
	switch {
	case c.fetch:
		if c.seen {
			if _, err := d.TaskDo(ctx, c.task, "seen", api.TaskRequest{Source: Source}); err != nil {
				return carried{}, err
			}
		}
		e, err := d.Task(ctx, c.task)
		if err != nil || e.Kind == "draft" {
			return carried{task: e}, err
		}
		ds, err := d.TaskDrafts(ctx, c.task)
		return carried{task: e, drafts: ds}, err
	case c.capture != "":
		e, err := d.CreateTask(ctx, api.TaskCreate{Title: c.capture})
		if err != nil {
			return carried{}, err
		}
		return carried{lines: []string{"in the Inbox as " + e.ID}, task: e}, nil
	case c.open != "" || c.openFind:
		lines, e, err := openLink(ctx, cfg, c)
		return carried{lines: lines, task: e}, err
	case c.verb != "":
		res, err := d.TaskDo(ctx, c.task, c.verb, api.TaskRequest{Args: c.args, Source: Source})
		return carried{lines: res.Lines, task: res.Task}, err
	case c.reviewStep != "":
		rv, err := d.ReviewStepDone(ctx, c.reviewStep)
		if err != nil || !rv.Done {
			return carried{}, err
		}
		return carried{lines: []string{"the review is done"}}, nil
	}
	lines, err := carryAction(ctx, d, c)
	return carried{lines: lines}, err
}

// openLink opens the command's link, looking it up first from the task's
// cards when the list had none.
func openLink(ctx context.Context, cfg Config, c Command) ([]string, api.TaskView, error) {
	if cfg.OpenURL == nil {
		return nil, api.TaskView{}, errors.New("opening links is not supported here")
	}
	link := c.open
	if link == "" {
		e, err := cfg.Daemon.Task(ctx, c.task)
		if err != nil {
			return nil, api.TaskView{}, err
		}
		for _, s := range e.Cards {
			if len(e.Units) > 0 && s.Card.Unit == e.Units[0] {
				link = s.Card.Link
			}
		}
		if link == "" {
			return nil, e, errors.New("its unit has no link")
		}
	}
	if !strings.HasPrefix(link, "https://") {
		return nil, api.TaskView{}, fmt.Errorf("not opening %q: only https links open", link)
	}
	if err := cfg.OpenURL(link); err != nil {
		return nil, api.TaskView{}, err
	}
	return []string{"opened " + link}, api.TaskView{}, nil
}

// carryAction takes a plan action to its item, any other to its unit.
func carryAction(ctx context.Context, d Daemon, c Command) ([]string, error) {
	if act, ok := planActs[c.Action]; ok && c.PlanID != "" {
		res, err := d.ActOnPlan(ctx, api.PlanActRequest{IDs: []string{c.PlanID}, Action: act, Guidance: c.Req.Guidance})
		switch {
		case err != nil:
			return nil, err
		case len(res.Errors) > 0:
			return nil, errors.New(strings.Join(res.Errors, "; "))
		case len(res.Items) == 0:
			return nil, nil
		}
		it := res.Items[0]
		return []string{strings.TrimSuffix(it.Decision+": "+it.Reason, ": ")}, nil
	}
	res, err := d.Ask(ctx, c.Unit, c.Action, c.Req)
	if res.Intake != nil {
		return []string{acceptedLine(*res.Intake)}, err
	}
	return res.Output, err
}

func deref(p *[]api.PlanItem) []api.PlanItem {
	if p == nil {
		return nil
	}
	return *p
}
