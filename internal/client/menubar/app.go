// Package menubar is the menu bar and notification layer as a client of the
// daemon's JSON: it decides the title, the menu and the notifications, and
// turns the user's answers into daemon actions. The native adapter lives in
// menubar/macos.
package menubar

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/api/client"
	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/client/rowset"
	"github.com/wloophq/wloop/internal/domain/gtd"
	"github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
)

// source is what every request of the menu bar and its notifications names,
// but an answer.
const source = "menubar"

// AnswerSource is what an answer from a notification names.
const AnswerSource = string(turn.Notification)

const (
	perSection = 5
	refreshMin = time.Second
	refreshMax = 30 * time.Second
)

type Daemon interface {
	RowsSince(ctx context.Context, since api.Since) (api.RowsPage, error)
	EntriesSince(ctx context.Context, q client.EntryQuery, since api.Since) (api.EntriesPage, error)
	Entry(ctx context.Context, id string) (api.EntryView, error)
	EntryDo(ctx context.Context, id, verb string, req api.EntryRequest) (api.EntryResult, error)
	Digest(ctx context.Context, day string) (api.Digest, error)
	Health(ctx context.Context) (api.Health, error)
	Action(ctx context.Context, u unit.Unit, action string, req api.ActionRequest) ([]string, error)
	Trigger(ctx context.Context, job string) error
	SetPause(ctx context.Context, req api.PauseRequest) error
	ReportApp(ctx context.Context, st api.AppStatus) error
	Events(ctx context.Context, client string, since api.Since) (<-chan api.Change, error)
	Actions(ctx context.Context) (api.ActionsResponse, error)
	Plugins(ctx context.Context) ([]api.Plugin, error)
}

type Config struct {
	Daemon   Daemon
	Tray     Tray
	Notifier Notifier
	OpenURL  func(url string) error
	// CockpitURL is the cockpit's address at a hash route such as
	// "entry=<id>" or "view=inbox".
	CockpitURL func(route string) (string, error)
	Quit       func()
	Now        func() time.Time
	// After is time.After unless a test supplies its own clock.
	After func(time.Duration) <-chan time.Time
	Log   func(format string, args ...any)
	// SentPath keeps today's held notifications and digest across
	// restarts; "" keeps them in memory only.
	SentPath string
	// DigestAt is the local "HH:MM" of the daily digest; "" posts none.
	DigestAt string
}

type Controller struct {
	cfg  Config
	ctx  context.Context
	mu   sync.Mutex
	sent map[string]bool
	// daily are today's held entries and digest already posted.
	daily    map[string]bool
	posted   map[string]posted
	saveMu   sync.Mutex
	perm     Permission
	reported bool
	specs    api.ActionsResponse

	refreshMu sync.Mutex
	set       rowset.Set
	health    api.Health
	hasHealth bool
	sections  []api.MenubarSection
	entries   map[gtd.View][]api.EntryView
	counters  *api.Counters
}

func New(cfg Config) *Controller {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.After == nil {
		cfg.After = time.After
	}
	if cfg.Log == nil {
		cfg.Log = func(string, ...any) {}
	}
	c := &Controller{cfg: cfg, ctx: context.Background(), sent: map[string]bool{}, daily: map[string]bool{}, entries: map[gtd.View][]api.EntryView{}}
	cfg.Notifier.OnResponse(func(r Response) { c.Respond(c.runCtx(), r) })
	c.loadSent()
	return c
}

func (c *Controller) runCtx() context.Context {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.ctx
}

// Run draws the menu, reports the app to the daemon, asks for the
// notification permission without waiting for the answer, and follows the
// daemon's changes, pulling only the rows that moved. It polls every
// refreshMax only while the change stream is down.
func (c *Controller) Run(ctx context.Context) error {
	c.mu.Lock()
	c.ctx = ctx
	c.mu.Unlock()
	c.cfg.Notifier.WithdrawAll()
	c.forgetSent()
	if c.cfg.DigestAt != "" {
		go c.digests(ctx)
	}
	go func() {
		if _, err := c.cfg.Notifier.Authorize(ctx); err != nil && ctx.Err() == nil {
			c.cfg.Log("notifications: %v", err)
		}
		c.checkPermission(ctx)
	}()
	for {
		started := c.cfg.Now()
		c.refresh(ctx, true, true)
		c.checkPermission(ctx)
		events, err := c.cfg.Daemon.Events(ctx, "app", c.since())
		if err != nil {
			if !c.sleep(ctx, refreshMax) {
				return nil
			}
			continue
		}
		if !c.follow(ctx, events) {
			return nil
		}
		if !c.sleep(ctx, refreshMin-c.cfg.Now().Sub(started)) {
			return nil
		}
	}
}

func (c *Controller) since() api.Since {
	c.refreshMu.Lock()
	defer c.refreshMu.Unlock()
	return c.set.Since()
}

// sleep waits d; false when ctx is done first.
func (c *Controller) sleep(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return ctx.Err() == nil
	}
	select {
	case <-ctx.Done():
		return false
	case <-c.cfg.After(d):
		return true
	}
}

// checkPermission reports the notification setting when it changed, so
// turning notifications off in System Settings reaches doctor.
func (c *Controller) checkPermission(ctx context.Context) {
	perm, err := c.cfg.Notifier.Permission(ctx)
	if err != nil {
		c.cfg.Log("notifications: %v", err)
		return
	}
	c.mu.Lock()
	same := c.reported && perm == c.perm
	c.perm, c.reported = perm, true
	c.mu.Unlock()
	if !same {
		c.report(ctx, api.AppStatus{State: "running", Notifications: string(perm)})
	}
}

// follow refreshes on each change until the stream ends, at most once a
// refreshMin; false when ctx is done.
func (c *Controller) follow(ctx context.Context, events <-chan api.Change) bool {
	var last time.Time
	for {
		select {
		case <-ctx.Done():
			return false
		case ch, ok := <-events:
			if !ok {
				return true
			}
			if !c.sleep(ctx, refreshMin-c.cfg.Now().Sub(last)) {
				return false
			}
			last = c.cfg.Now()
			c.apply(ctx, ch)
			c.checkPermission(ctx)
		}
	}
}

// apply pulls the rows and the entries past the held revision, unless the
// change only names the health and is not ahead of it, and the health only
// when the change names it.
func (c *Controller) apply(ctx context.Context, ch api.Change) {
	for _, n := range ch.Notices {
		c.postNotice(n)
	}
	if ch.Counters != nil {
		c.refreshMu.Lock()
		counters := *ch.Counters
		c.counters = &counters
		c.refreshMu.Unlock()
	}
	held := c.since()
	behind := !ch.All && len(ch.Units) == 0 && ch.Epoch == held.Epoch && ch.Rev <= held.Rev
	if behind && !ch.Health && len(ch.Notices) > 0 {
		return
	}
	c.refresh(ctx, ch.Health, !(behind && ch.Health))
}

func (c *Controller) report(ctx context.Context, st api.AppStatus) {
	if err := c.cfg.Daemon.ReportApp(ctx, st); err != nil {
		c.cfg.Log("reporting the app status: %v", err)
	}
}

// Refresh reads the rows and entries that moved and the health, then
// redraws the title, the menu and the notifications.
func (c *Controller) Refresh(ctx context.Context) { c.refresh(ctx, true, true) }

func (c *Controller) refresh(ctx context.Context, health, rows bool) {
	c.refreshMu.Lock()
	defer c.refreshMu.Unlock()
	var err error
	if rows {
		if err = c.pullRows(ctx); err == nil {
			err = c.pullEntries(ctx)
		}
	}
	if err == nil && (health || !c.hasHealth) {
		var h api.Health
		if h, err = c.cfg.Daemon.Health(ctx); err == nil {
			c.health = h
			err = c.loadSections(ctx)
		}
		c.hasHealth = c.hasHealth || err == nil
	}
	var shown []api.Row
	for _, sec := range c.sections {
		shown = append(shown, c.set.View(sec.View)...)
	}
	if err == nil {
		err = c.loadSpecs(ctx, shown)
	}
	if err != nil {
		c.cfg.Tray.SetTitle("PR ?")
		c.cfg.Tray.SetMenu([]MenuItem{
			{Title: "Daemon not answering: " + err.Error()},
			{Separator: true},
			{Title: "Quit " + brand.Name, Enabled: true, Action: c.cfg.Quit},
		})
		return
	}
	c.cfg.Tray.SetTitle(c.title())
	c.cfg.Tray.SetMenu(c.menu(c.health))
	c.notify()
}

// loadSections reads the menu bar sections of the enabled plugins, in the
// plugins' order; the daemon reads its plugins at start, so they change
// with the health a restart reports.
func (c *Controller) loadSections(ctx context.Context) error {
	ps, err := c.cfg.Daemon.Plugins(ctx)
	if err != nil {
		return err
	}
	c.sections = nil
	for _, p := range ps {
		if p.Enabled && p.UI != nil {
			c.sections = append(c.sections, p.UI.Menubar...)
		}
	}
	return nil
}

// pullRows merges the daemon's page since the set's position; a page the
// set refuses is pulled again whole.
func (c *Controller) pullRows(ctx context.Context) error {
	page, err := c.cfg.Daemon.RowsSince(ctx, c.set.Since())
	if err != nil {
		return err
	}
	if c.set.Apply(page) {
		return nil
	}
	if page, err = c.cfg.Daemon.RowsSince(ctx, api.Since{}); err != nil {
		return err
	}
	c.set.Apply(page)
	return nil
}

// loadSpecs reads the specs again when a row follows a version other than
// the one held.
func (c *Controller) loadSpecs(ctx context.Context, rows []api.Row) error {
	c.mu.Lock()
	held := c.specs
	c.mu.Unlock()
	if held.Version != "" && !slices.ContainsFunc(rows, func(r api.Row) bool { return r.SpecsVersion != held.Version }) {
		return nil
	}
	specs, err := c.cfg.Daemon.Actions(ctx)
	if err != nil {
		return err
	}
	c.mu.Lock()
	c.specs = specs
	c.mu.Unlock()
	return nil
}

func (c *Controller) spec(id string) (api.ActionSpec, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	i := slices.IndexFunc(c.specs.Specs, func(s api.ActionSpec) bool { return s.ID == id })
	if i < 0 {
		return api.ActionSpec{}, false
	}
	return c.specs.Specs[i], true
}

func (c *Controller) menu(h api.Health) []MenuItem {
	var items []MenuItem
	if b := h.Briefs; b != nil && b.Unavailable != "" {
		items = append(items, MenuItem{Title: "briefs unavailable: " + b.Unavailable}, MenuItem{Separator: true})
	}
	if b := h.Briefs; b != nil && b.Open {
		items = append(items, MenuItem{Title: fmt.Sprintf("briefs unavailable since %s: %s", b.Since.Local().Format("15:04"), b.Error)}, MenuItem{Separator: true})
	}
	open := "Open workspace"
	if len(h.WorkspaceManagers) == 1 {
		open = "Open in " + h.WorkspaceManagers[0]
	}
	for _, sec := range coreSections {
		items = append(items, c.entrySection(sec.label, sec.view)...)
		items = append(items, MenuItem{Separator: true})
	}
	for _, sec := range c.sections {
		items = append(items, c.section(sec.Label, sec.View, c.set.View(sec.View), open)...)
		items = append(items, MenuItem{Separator: true})
	}
	items = append(items,
		MenuItem{Title: "Open cockpit", Enabled: true, Action: c.later(func(ctx context.Context) error { return c.open("view=" + string(gtd.ViewInbox)) })},
		MenuItem{Title: "Sweep now", Enabled: true, Action: c.later(func(ctx context.Context) error { return c.cfg.Daemon.Trigger(ctx, "sweep") })},
	)
	pause := MenuItem{Title: "Pause automatic rounds", Enabled: true, Action: c.later(func(ctx context.Context) error { return c.cfg.Daemon.SetPause(ctx, api.PauseRequest{Paused: true}) })}
	if h.Paused {
		pause = MenuItem{Title: "Resume automatic rounds", Enabled: true, Action: c.later(func(ctx context.Context) error { return c.cfg.Daemon.SetPause(ctx, api.PauseRequest{Paused: false}) })}
	}
	items = append(items, pause, MenuItem{Title: c.syncLine(h)}, MenuItem{Separator: true},
		MenuItem{Title: "Quit " + brand.Name, Enabled: true, Action: c.cfg.Quit})
	return items
}

func (c *Controller) section(name, view string, rows []api.Row, open string) []MenuItem {
	items := []MenuItem{{Title: fmt.Sprintf("%s (%d)", name, len(rows))}}
	for i, r := range rows {
		if i == perSection {
			items = append(items, MenuItem{Title: "more…", Enabled: true, Action: c.later(func(context.Context) error { return c.open(viewRoute(view)) })})
			break
		}
		items = append(items, c.rowItem(view, r, open))
	}
	return items
}

func (c *Controller) rowItem(view string, r api.Row, open string) MenuItem {
	action := func(context.Context) error { return c.open(viewRoute(view) + "&" + unitRoute(r.Unit)) }
	if r.Unit.Provider != unit.GitHub && strings.HasPrefix(r.Link, "https://") {
		action = func(context.Context) error { return c.cfg.OpenURL(r.Link) }
	}
	return MenuItem{
		Title:    r.Label + " · " + label(r),
		Enabled:  true,
		Action:   c.later(action),
		Children: c.rowActions(r, open),
	}
}

// rowActions are the actions the daemon offers on the row for the menu bar,
// in the order it serves them. A menu cannot take text, so an action that
// asks for input is left to the other surfaces.
func (c *Controller) rowActions(r api.Row, open string) []MenuItem {
	var items []MenuItem
	for _, st := range r.Actions {
		spec, ok := c.spec(st.ID)
		if !ok || !slices.Contains(spec.Surfaces, "menubar") || spec.Input != nil {
			continue
		}
		title := cmp.Or(st.Label, spec.Label)
		if st.ID == "open" {
			title = open
		}
		if !st.Enabled {
			title += " (" + st.Reason + ")"
			items = append(items, MenuItem{Title: title})
			continue
		}
		do := c.later(func(ctx context.Context) error {
			_, err := c.cfg.Daemon.Action(ctx, r.Unit, st.ID, api.ActionRequest{Source: source, Basis: st.Basis})
			return err
		})
		if st.Confirm == "" {
			items = append(items, MenuItem{Title: title, Enabled: true, Action: do})
			continue
		}
		items = append(items, MenuItem{Title: title + "…", Enabled: true, Children: []MenuItem{{Title: st.Confirm, Enabled: true, Action: do}}})
	}
	return items
}

func label(r api.Row) string { return r.Summary.Line }

func (c *Controller) syncLine(h api.Health) string {
	line := "Not synced yet"
	if at := syncedAt(h.Jobs); !at.IsZero() {
		line = "Synced " + at.Local().Format("15:04")
	}
	if h.RateLimit.Limit > 0 {
		line += fmt.Sprintf(" · %d/%d", h.RateLimit.Remaining, h.RateLimit.Limit)
	}
	return line
}

// syncedAt is when every source last ingested, zero until each has.
func syncedAt(jobs []api.JobStatus) time.Time {
	var at time.Time
	for i, j := range slices.DeleteFunc(slices.Clone(jobs), func(j api.JobStatus) bool { return !strings.HasPrefix(j.Name, "ingest:") }) {
		if j.LastOK.IsZero() {
			return time.Time{}
		}
		if i == 0 || j.LastOK.Before(at) {
			at = j.LastOK
		}
	}
	return at
}

// later runs fn off the caller's goroutine, so a menu click never blocks
// the main thread on the daemon.
func (c *Controller) later(fn func(ctx context.Context) error) func() {
	return func() {
		go func() {
			if err := fn(c.runCtx()); err != nil {
				c.cfg.Log("%v", err)
			}
		}()
	}
}

func (c *Controller) open(route string) error {
	u, err := c.cfg.CockpitURL(route)
	if err != nil {
		return err
	}
	return c.cfg.OpenURL(u)
}
