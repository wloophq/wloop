// Package window decides when and where the cockpit's native window and its
// omnibox panel show; a Host draws them.
package window

import (
	"context"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/wloophq/wloop/internal/api"
)

// PanelRoute is the cockpit route the panel loads.
const PanelRoute = "panel=omnibox"

// reconnectAfter is the wait before subscribing again to a daemon that went
// away, the same as the app's wait for the daemon to start.
const reconnectAfter = 200 * time.Millisecond

// Host shows the cockpit natively. Every method returns at once; the host
// runs AppKit work on the main thread.
type Host interface {
	// ShowMain shows, focuses and brings the app forward, in the Dock and
	// ⌘Tab while shown.
	ShowMain(url string)
	// ShowPanel shows the panel without activating the app.
	ShowPanel(url string)
	HidePanel()
	// ShowDetached shows and focuses the secondary window id, opening it at
	// url when none is open; it closes for good and is not restored at
	// relaunch.
	ShowDetached(id, url string)
	// OpenExternal opens url, a web address, in the default browser.
	OpenExternal(url string)
	// Listen binds the hotkey, the reopen and opened links; an error leaves
	// the reopen and the links bound.
	Listen(h Handlers) error
}

// Handlers are what a host calls back.
type Handlers struct {
	// Hotkey is an accelerator such as "ctrl+option+space"; "" binds none.
	Hotkey   string
	OnHotkey func()
	// OnReopen runs when the Dock icon is clicked or the bundle is opened
	// again while running.
	OnReopen func()
	// OnPanelHidden runs when the host hid the panel on its own, as on
	// focus loss.
	OnPanelHidden func()
	// OnOpenURL runs for each deep link the system hands the app.
	OnOpenURL func(link string)
}

// Events is the daemon's events stream.
type Events interface {
	Events(ctx context.Context, client string, since api.Since) (<-chan api.Change, error)
}

// Config is what a Controller needs.
type Config struct {
	Host       Host
	Daemon     Events
	CockpitURL func(route string) (string, error)
	Hotkey     string
	Log        func(format string, args ...any)
	// After is time.After unless a test supplies its own.
	After func(time.Duration) <-chan time.Time
}

// Controller keeps one main window and one panel.
type Controller struct {
	cfg        Config
	mu         sync.Mutex
	panelShown bool
}

// New returns a controller of cfg.Host.
func New(cfg Config) *Controller {
	if cfg.Log == nil {
		cfg.Log = func(string, ...any) {}
	}
	if cfg.After == nil {
		cfg.After = time.After
	}
	return &Controller{cfg: cfg}
}

// Run binds the host's handlers, then follows the window requests the daemon
// relays until ctx ends, subscribing again whenever the daemon goes away; it
// returns once the stream it follows has closed.
func (c *Controller) Run(ctx context.Context) error {
	err := c.cfg.Host.Listen(Handlers{
		Hotkey:        c.cfg.Hotkey,
		OnHotkey:      c.TogglePanel,
		OnReopen:      func() { _ = c.Show("") },
		OnPanelHidden: func() { c.setPanel(false) },
		OnOpenURL:     c.OpenLink,
	})
	if err != nil {
		c.cfg.Log("window.hotkey %q: %v", c.cfg.Hotkey, err)
	}
	for {
		if changes, err := c.cfg.Daemon.Events(ctx, api.WindowClient, api.Since{}); err == nil {
			c.follow(ctx, changes)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-c.cfg.After(reconnectAfter):
		}
	}
}

func (c *Controller) follow(ctx context.Context, changes <-chan api.Change) {
	for {
		select {
		case <-ctx.Done():
			for range changes {
			}
			return
		case ch, open := <-changes:
			if !open {
				return
			}
			for _, req := range ch.Windows {
				c.handle(req)
			}
		}
	}
}

func (c *Controller) handle(req api.WindowRequest) {
	switch req.Action {
	case api.WindowOpen:
		_ = c.Show(req.Route)
	case api.WindowPanel:
		c.showPanel()
	case api.WindowHidePanel:
		c.hidePanel()
	case api.WindowDetach:
		c.detach(req.Route)
	case api.WindowExternal:
		c.cfg.Host.OpenExternal(req.URL)
	}
}

// OpenLink shows a deep link's route in the main window; a link it cannot
// read shows the default view, which says so.
func (c *Controller) OpenLink(link string) {
	route, ok := LinkRoute(link)
	if !ok {
		c.cfg.Log("window: %q is not a link the app opens", link)
		route = MalformedLinkRoute
	}
	_ = c.Show(route)
}

// detach shows the item route names in its own window, one per item; a
// route that names none opens in the main window.
func (c *Controller) detach(route string) {
	item := itemOf(route)
	if item == "" {
		_ = c.Show(route)
		return
	}
	u, err := c.url(item + "&" + detailLayout)
	if err != nil {
		return
	}
	c.cfg.Host.ShowDetached(item, u)
}

// Show shows the main window at route ("" keeps where it was) and hides the
// panel; a panel route shows the panel instead, since the panel's page in
// the main window would hand every other route back to it.
func (c *Controller) Show(route string) error {
	if panelRoute(route) {
		c.showPanel()
		return nil
	}
	u, err := c.url(withoutDetailLayout(route))
	if err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cfg.Host.ShowMain(u)
	c.cfg.Host.HidePanel()
	c.panelShown = false
	return nil
}

// TogglePanel shows the panel, or hides it when it shows.
func (c *Controller) TogglePanel() {
	c.mu.Lock()
	shown := c.panelShown
	c.mu.Unlock()
	if shown {
		c.hidePanel()
		return
	}
	c.showPanel()
}

func (c *Controller) showPanel() {
	u, err := c.url(PanelRoute)
	if err != nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cfg.Host.ShowPanel(u)
	c.panelShown = true
}

func (c *Controller) hidePanel() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cfg.Host.HidePanel()
	c.panelShown = false
}

func (c *Controller) setPanel(shown bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.panelShown = shown
}

func panelRoute(route string) bool {
	q, _ := url.ParseQuery(route)
	return q.Get("panel") != ""
}

// detailLayout is the route flag of a window that shows one item's detail
// alone.
const detailLayout = "layout=detail"

// itemOf is the parameter of route naming the item its detail shows, the
// task before the unit, as the cockpit does; "" when it names none.
func itemOf(route string) string {
	parts := strings.Split(route, "&")
	for _, name := range []string{"task=", "unit="} {
		for _, p := range parts {
			if strings.HasPrefix(p, name) && len(p) > len(name) {
				return p
			}
		}
	}
	return ""
}

func withoutDetailLayout(route string) string {
	parts := strings.Split(route, "&")
	return strings.Join(slices.DeleteFunc(parts, func(p string) bool { return p == detailLayout }), "&")
}

func (c *Controller) url(route string) (string, error) {
	u, err := c.cfg.CockpitURL(route)
	if err != nil {
		c.cfg.Log("window: %v", err)
	}
	return u, err
}
