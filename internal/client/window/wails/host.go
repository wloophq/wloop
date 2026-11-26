//go:build darwin && purego

package wails

import (
	"encoding/json"
	"errors"
	"math"
	"net/url"
	"os"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ebitengine/purego/objc"
	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"

	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/client/texts"
	"github.com/wloophq/wloop/internal/client/window"
)

const (
	activationPolicyRegular   = 0
	activationPolicyAccessory = 1
)

var frameAutosaveName = brand.LaunchAgent() + ".window"

// Available reports whether this binary can host the window.
func Available() bool { return true }

// Run starts the app with a hidden main window and a hidden panel, calls
// ready on the main thread once it has launched, and runs the loop. It must
// be called from the main goroutine, which the main package pins to the main
// thread, and never returns: quitting the app ends the process.
func Run(name string, cat *texts.Catalog, ready func(window.Host)) {
	runtime.LockOSThread()
	var h *host
	app := application.New(application.Options{
		Name: name,
		Mac: application.MacOptions{
			ActivationPolicy: application.ActivationPolicyAccessory,
			ApplicationShouldTerminateAfterLastWindowClosed: false,
		},
		RawMessageHandler: func(w application.Window, message string, origin *application.OriginInfo) {
			if origin != nil {
				h.pageMessage(w.Name(), message, origin.Origin, origin.IsMainFrame)
			}
		},
	})
	app.Menu.Set(wailsMenu(menuOf(name, cat)))
	h = newHost(app, name)
	app.Event.OnApplicationEvent(events.Mac.ApplicationDidFinishLaunching, func(*application.ApplicationEvent) {
		go application.InvokeSync(func() {
			h.launched()
			ready(h)
		})
	})
	app.Event.OnApplicationEvent(events.Mac.ApplicationShouldHandleReopen, func(*application.ApplicationEvent) {
		if on := h.handlers().OnReopen; on != nil {
			go on()
		}
	})
	app.Event.OnApplicationEvent(events.Common.ApplicationLaunchedWithUrl, func(e *application.ApplicationEvent) {
		h.openURL(e.Context().URL())
	})
	app.Event.OnApplicationEvent(events.Mac.ApplicationDidChangeScreenParameters, func(*application.ApplicationEvent) {
		h.do(h.screensChanged)
	})
	_ = app.Run()
	h.stop()
	os.Exit(0)
}

func wailsMenu(items []menuItem) *application.Menu {
	var tops []*application.MenuItem
	for _, top := range items {
		var subs []*application.MenuItem
		for _, it := range top.items {
			if it.role == roleSeparator {
				subs = append(subs, application.NewMenuItemSeparator())
				continue
			}
			subs = append(subs, application.NewRole(wailsRole[it.role]).SetLabel(it.title))
		}
		tops = append(tops, application.NewSubmenu(top.title, application.NewMenuFromItems(subs[0], subs[1:]...)))
	}
	return application.NewMenuFromItems(tops[0], tops[1:]...)
}

var wailsRole = map[role]application.Role{
	roleHide:       application.Hide,
	roleHideOthers: application.HideOthers,
	roleShowAll:    application.ShowAll,
	roleQuit:       application.Quit,
	roleUndo:       application.Undo,
	roleRedo:       application.Redo,
	roleCut:        application.Cut,
	roleCopy:       application.Copy,
	rolePaste:      application.Paste,
	roleSelectAll:  application.SelectAll,
	roleMinimise:   application.Minimise,
	roleZoom:       application.Zoom,
	roleClose:      application.CloseWindow,
}

// host runs every request in order on its own goroutine, so a caller never
// waits on the main thread.
type host struct {
	userDefaults
	app                *application.App
	name               string
	main, panel, float *application.WebviewWindow

	mu        sync.Mutex
	queue     []func()
	wake      chan struct{}
	listening window.Handlers
	// done stops the worker and every release poll; running is them, which
	// stop waits for.
	done    chan struct{}
	running sync.WaitGroup
	// native is how the host reads and moves the float's tab in AppKit;
	// panelNative the panel window's, made by makePanel on its first load.
	native, panelNative floatNative
	makePanel           func() floatNative

	// links orders the deep links handed to the controller; pending are
	// those that came before it listened.
	links   sync.Mutex
	pending []string

	// Touched only by the worker.
	mainURL, panelURL string
	mainShown         bool
	detached          map[string]*detachedWindow
	panelShown        bool
	// panelKey says the panel became key since it was last shown, so a
	// focus loss is the person leaving it, not a late event of an earlier
	// show.
	panelKey                bool
	floatURL, floatPanelURL string
	// floatShown, floatFrame, the tab's frame last set or dropped, and
	// dragging, from the first move the person made to the drop.
	floatShown bool
	floatFrame window.Rect
	dragging   bool
	// panelMode is the mode the panel's page last said it draws in, "" while
	// closed; overTab and overPanel say the pointer is over each window, and
	// grace counts the pointer's moves, so a grace a later move overtook
	// does nothing.
	panelMode          string
	overTab, overPanel bool
	grace              int
	// panelKeyable is read on the main thread, where AppKit asks whether the
	// panel window may become key.
	panelKeyable atomic.Bool
}

// The names of the float's tab window and its panel window.
const (
	floatName      = "float"
	floatPanelName = "float-panel"
)

// floatNative is a float window's AppKit side the host reads and acts on.
type floatNative struct {
	buttonsUp func() bool
	frame     func() window.Rect
	run       func(js string)
	load      func(url string)
	show      func()
	hide      func()
	setFrame  func(r window.Rect)
}

func appKitFloat(w *application.WebviewWindow) floatNative {
	return floatNative{
		buttonsUp: mouseButtonsUp,
		frame: func() (f window.Rect) {
			application.InvokeSync(func() { f = frameOf(nsWindow(w)) })
			return f
		},
		run:      func(js string) { evalJS(w, js) },
		load:     func(url string) { w.SetURL(url) },
		show:     func() { w.Show() },
		hide:     func() { w.Hide() },
		setFrame: func(r window.Rect) { application.InvokeSync(func() { setFrame(nsWindow(w), r) }) },
	}
}

func newHost(app *application.App, name string) *host {
	h := &host{app: app, name: name, detached: map[string]*detachedWindow{}}
	h.main = app.Window.NewWithOptions(application.WebviewWindowOptions{
		Name: "main", Title: name, URL: "about:blank", Width: 1280, Height: 820, Hidden: true,
		Mac: insetTitleBar,
	})
	h.panel = app.Window.NewWithOptions(panelOptions(name))
	h.float = app.Window.NewWithOptions(floatOptions(name, floatName))
	h.native = appKitFloat(h.float)
	h.makePanel = h.newFloatPanel
	h.float.RegisterHook(events.Common.WindowClosing, func(e *application.WindowEvent) { e.Cancel() })
	h.float.OnWindowEvent(events.Mac.WindowDidMove, func(*application.WindowEvent) { h.do(h.floatMoved) })
	h.float.OnWindowEvent(events.Mac.WebViewWebContentProcessDidTerminate, func(*application.WindowEvent) { h.do(func() { h.floatGone(window.FloatTab) }) })
	h.main.RegisterHook(events.Common.WindowClosing, func(e *application.WindowEvent) {
		e.Cancel()
		h.do(h.hideMain)
	})
	h.panel.RegisterHook(events.Common.WindowClosing, func(e *application.WindowEvent) {
		e.Cancel()
		h.do(func() { h.panelLeft(true) })
	})
	h.panel.OnWindowEvent(events.Mac.WindowDidBecomeKey, func(*application.WindowEvent) {
		h.do(func() { h.panelKey = h.panelShown })
	})
	h.panel.OnWindowEvent(events.Common.WindowLostFocus, func(*application.WindowEvent) {
		h.do(func() { h.panelLeft(false) })
	})
	h.start()
	return h
}

// panelOptions are the omnibox panel's. AlwaysOnTop keeps its level:
// Wails sets the level of a window created hidden from AlwaysOnTop as it
// first becomes key.
func panelOptions(name string) application.WebviewWindowOptions {
	return application.WebviewWindowOptions{
		Name: "panel", Title: name, URL: "about:blank", Width: 680, Height: 420, Hidden: true, Frameless: true,
		InitialPosition: application.WindowCentered, AlwaysOnTop: true,
		Mac: application.MacWindow{
			WindowClass:      application.MacWindowClassPanel,
			PanelPreferences: application.MacPanelPreferences{NonActivating: true, FloatingPanel: true},
			WindowLevel:      application.MacWindowLevelFloating,
			CollectionBehavior: application.MacWindowCollectionBehaviorCanJoinAllSpaces |
				application.MacWindowCollectionBehaviorFullScreenAuxiliary,
		},
	}
}

// floatOptions are those of the floating widget's window named window, kept
// on top as panelOptions are.
func floatOptions(name, window string) application.WebviewWindowOptions {
	return application.WebviewWindowOptions{
		Name: window, Title: name, URL: "about:blank", Width: 112, Height: 32, Hidden: true, Frameless: true,
		DisableResize: true, BackgroundType: application.BackgroundTypeTransparent, AlwaysOnTop: true,
		Mac: application.MacWindow{
			WindowClass:      application.MacWindowClassPanel,
			PanelPreferences: application.MacPanelPreferences{NonActivating: true, FloatingPanel: true},
			WindowLevel:      application.MacWindowLevelFloating,
			CollectionBehavior: application.MacWindowCollectionBehaviorCanJoinAllSpaces |
				application.MacWindowCollectionBehaviorStationary | application.MacWindowCollectionBehaviorIgnoresCycle,
			Backdrop:      application.MacBackdropTransparent,
			DisableShadow: true,
		},
	}
}

// start runs the worker until stop.
func (h *host) start() {
	h.wake, h.done = make(chan struct{}, 1), make(chan struct{})
	h.running.Go(h.work)
}

// quitGrace is how long quitting waits for the worker: work that waits on the
// main thread, whose loop app.Run has ended, never returns.
const quitGrace = 2 * time.Second

// stop ends the worker and every release poll, and waits for them, at most
// quitGrace.
func (h *host) stop() {
	close(h.done)
	stopped := make(chan struct{})
	go func() {
		h.running.Wait()
		close(stopped)
	}()
	t := time.NewTimer(quitGrace)
	defer t.Stop()
	select {
	case <-stopped:
	case <-t.C:
	}
}

// launched restores the main window's frame and readies the float's tab for
// the mouse, never to take the keyboard; it runs on the main thread.
func (h *host) launched() {
	nsWindow(h.main).Send(objc.RegisterName("setFrameAutosaveName:"), nsString(frameAutosaveName))
	floatOnly(nsWindow(h.float), func() bool { return false }, h.pointerIn(window.FloatTab), h.app.Logger)
}

// newFloatPanel makes the float's panel window, hidden, readied for the
// mouse and to take the keyboard only while it shows the full panel; it runs
// on the worker.
func (h *host) newFloatPanel() floatNative {
	w := h.app.Window.NewWithOptions(floatOptions(h.name, floatPanelName))
	w.RegisterHook(events.Common.WindowClosing, func(e *application.WindowEvent) { e.Cancel() })
	w.OnWindowEvent(events.Mac.WindowDidResignKey, func(*application.WindowEvent) { h.do(h.panelResigned) })
	w.OnWindowEvent(events.Mac.WebViewWebContentProcessDidTerminate, func(*application.WindowEvent) { h.do(func() { h.floatGone(window.FloatPanel) }) })
	over := h.handlers().FloatOverFullScreen
	application.InvokeSync(func() {
		floatOnly(nsWindow(w), h.panelKeyable.Load, h.pointerIn(window.FloatPanel), h.app.Logger)
		if over {
			addCollectionBehavior(nsWindow(w), application.MacWindowCollectionBehaviorFullScreenAuxiliary)
		}
	})
	return appKitFloat(w)
}

// pointerIn is what the tracking of the window named win reports: the
// pointer entering or leaving it.
func (h *host) pointerIn(win string) func(inside bool) {
	return func(inside bool) { h.do(func() { h.pointer(win, inside) }) }
}

func (h *host) do(fn func()) {
	h.mu.Lock()
	h.queue = append(h.queue, fn)
	h.mu.Unlock()
	select {
	case h.wake <- struct{}{}:
	default:
	}
}

func (h *host) work() {
	for {
		select {
		case <-h.done:
			return
		case <-h.wake:
		}
		for {
			h.mu.Lock()
			if len(h.queue) == 0 {
				h.mu.Unlock()
				break
			}
			fn := h.queue[0]
			h.queue = h.queue[1:]
			h.mu.Unlock()
			fn()
		}
	}
}

func (h *host) handlers() window.Handlers {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.listening
}

// insetTitleBar draws the window controls over the page, which draws the
// title bar around them (window.InsetTitleBar marks its URL).
var insetTitleBar = application.MacWindow{TitleBar: application.MacTitleBarHiddenInset}

// Reword words the app's menus from cat.
func (h *host) Reword(cat *texts.Catalog) {
	application.InvokeAsync(func() { h.app.Menu.Set(wailsMenu(menuOf(h.name, cat))) })
}

func (h *host) ShowMain(url string) {
	h.do(func() {
		navigate(h.main, &h.mainURL, window.InsetTitleBar(url))
		h.mainShown = true
		bringForward(h.main)
	})
}

// bringForward shows w, focused, with the app in the Dock and ⌘Tab.
func bringForward(w *application.WebviewWindow) {
	setActivationPolicy(activationPolicyRegular)
	if w.IsMinimised() {
		w.UnMinimise()
	}
	w.Show()
	application.InvokeSync(func() {
		nsApp().Send(objc.RegisterName("activateIgnoringOtherApps:"), true)
		nsWindow(w).Send(objc.RegisterName("makeKeyAndOrderFront:"), objc.ID(0))
	})
}

// detachedWindow is a secondary window and the URL it last loaded.
type detachedWindow struct {
	w   *application.WebviewWindow
	url string
}

// ShowDetached focuses the window id, reloading it only when the daemon
// moved to another port or token, or opens it at url. Closing it destroys it.
func (h *host) ShowDetached(id, url string) {
	h.do(func() {
		url = window.InsetTitleBar(url)
		d := h.detached[id]
		if d == nil {
			d = &detachedWindow{url: url, w: h.app.Window.NewWithOptions(application.WebviewWindowOptions{
				Name: "detached " + id, Title: h.name, URL: url, Width: 760, Height: 860, Hidden: true,
				InitialPosition: application.WindowCentered, Mac: insetTitleBar,
			})}
			h.detached[id] = d
			d.w.OnWindowEvent(events.Common.WindowClosing, func(*application.WindowEvent) {
				h.do(func() { h.closedDetached(id, d) })
			})
		} else if load, _ := window.Navigation(d.url, url); load {
			d.w.SetURL(url)
			d.url = url
		}
		bringForward(d.w)
	})
}

func (h *host) closedDetached(id string, d *detachedWindow) {
	if h.detached[id] == d {
		delete(h.detached, id)
	}
	h.settlePolicy()
}

// settlePolicy leaves the Dock and ⌘Tab once no window shows.
func (h *host) settlePolicy() {
	if !h.mainShown && len(h.detached) == 0 {
		setActivationPolicy(activationPolicyAccessory)
	}
}

func (h *host) ShowPanel(url string) {
	h.do(func() {
		navigate(h.panel, &h.panelURL, url)
		h.panelShown, h.panelKey = true, false
		application.InvokeSync(func() { centerOnFocusedScreen(h.panel) })
		h.panel.Show()
		application.InvokeSync(func() { nsWindow(h.panel).Send(objc.RegisterName("makeKeyWindow")) })
	})
}

func (h *host) OpenExternal(url string) {
	if err := h.app.Browser.OpenURL(url); err != nil {
		h.app.Logger.Warn("window: opening an outside link", "error", err)
	}
}

func (h *host) ShowFloat(url string, reload bool) {
	h.do(func() {
		if reload {
			h.float.SetURL(url)
			h.floatURL = url
		} else {
			navigate(h.float, &h.floatURL, url)
		}
		h.floatShown = true
		h.native.show()
	})
}

func (h *host) HideFloat() {
	h.do(func() {
		h.floatShown, h.overTab = false, false
		h.native.hide()
		h.hidePanel()
	})
}

func (h *host) SetFloatFrame(r window.Rect) {
	h.do(func() {
		h.floatFrame = r
		h.native.setFrame(r)
	})
}

// PlaceFloat tells the tab's page where it sits.
func (h *host) PlaceFloat(p window.FloatPlace) {
	h.do(func() { h.native.run(floatPlaceJS(p)) })
}

// LoadFloatPanel loads url in the panel window, making it on the first load.
func (h *host) LoadFloatPanel(url string) {
	h.do(func() {
		if h.panelNative.load == nil {
			h.panelNative = h.makePanel()
		}
		h.floatPanelURL = url
		h.panelNative.load(url)
	})
}

// TellFloatPanel tells the panel's page what to show.
func (h *host) TellFloatPanel(p window.PanelPlace) {
	h.do(func() {
		if h.panelNative.run != nil {
			h.panelNative.run(panelShowJS(p))
		}
	})
}

// ShowFloatPanel frames the panel window at r and orders it front in mode;
// a non-activating panel shown this way does not take the keyboard.
func (h *host) ShowFloatPanel(r window.Rect, mode string) {
	h.do(func() { h.showPanel(r, mode) })
}

// showPanel shows the panel window at r in mode, which alone decides whether
// it may become key: the controller shows it only at a size its page drew
// for what it was last asked, so a late size of the page never does.
func (h *host) showPanel(r window.Rect, mode string) {
	if h.panelNative.show == nil {
		return
	}
	h.panelNative.setFrame(r)
	h.panelNative.show()
	h.setPanelMode(mode)
}

func (h *host) HideFloatPanel() { h.do(h.hidePanel) }

// hidePanel hides the panel window, which hands back the keyboard it held,
// and forgets its mode and the pointer over it.
func (h *host) hidePanel() {
	if h.panelNative.hide == nil {
		return
	}
	h.panelNative.hide()
	h.overPanel = false
	h.setPanelMode("")
}

// panelResigned tells the full panel's page that its window lost the
// keyboard, as the person moved to another app, so a panel not pinned
// closes; WebKit may not fire the page's blur in a non-activating panel.
func (h *host) panelResigned() {
	if h.panelMode == window.ModePanel {
		h.panelNative.run(bridgeJS("blur"))
	}
}

// floatGrace is how long the pointer may be off both float windows before
// the full panel's page is asked to leave: crossing from the tab to the
// panel, or back, leaves one before it enters the other.
const floatGrace = 150 * time.Millisecond

// pointer follows the pointer entering or leaving the float's window win.
// Once it is over neither while the full panel shows, the panel's page is
// asked to leave after floatGrace, unless the pointer entered either since;
// the page closes unless pinned. It runs on the worker, which running counts.
func (h *host) pointer(win string, inside bool) {
	switch win {
	case window.FloatTab:
		h.overTab = inside
	case window.FloatPanel:
		h.overPanel = inside
	}
	h.grace++
	h.startGrace()
}

// startGrace asks the full panel's page to leave after floatGrace while the
// pointer is over neither window, unless it moves before then.
func (h *host) startGrace() {
	if h.overTab || h.overPanel || h.panelMode != window.ModePanel {
		return
	}
	at := h.grace
	h.running.Go(func() {
		select {
		case <-h.done:
		case <-time.After(floatGrace):
			h.do(func() {
				if at == h.grace && h.panelMode == window.ModePanel {
					h.panelNative.run(bridgeJS("leave"))
				}
			})
		}
	})
}

// bridgeJS calls method on the page's bridge with args, each as JSON.
func bridgeJS(method string, args ...any) string {
	name, _ := json.Marshal(window.FloatBridge)
	parts := make([]string, len(args))
	for i, a := range args {
		b, _ := json.Marshal(a)
		parts[i] = string(b)
	}
	return "(function(b){if(b&&b." + method + ")b." + method + "(" + strings.Join(parts, ",") + ")})(window[" + string(name) + "])"
}

// floatPlaceJS tells the tab's page p.
func floatPlaceJS(p window.FloatPlace) string {
	return bridgeJS("place", struct {
		Edge string `json:"edge"`
	}{p.Edge})
}

// panelShowJS tells the panel's page to show p.
func panelShowJS(p window.PanelPlace) string {
	return bridgeJS("show", p.Mode, struct {
		Item string  `json:"item"`
		Side string  `json:"side"`
		MaxW float64 `json:"maxW"`
		MaxH float64 `json:"maxH"`
	}{p.Item, p.Side, p.MaxW, p.MaxH})
}

// pageMessage hands the controller a message one of the float's pages sent
// from the origin its window loaded, in its main frame, naming the window it
// came from; any other message is dropped.
func (h *host) pageMessage(windowName, message, origin string, mainFrame bool) {
	from := map[string]string{floatName: window.FloatTab, floatPanelName: window.FloatPanel}[windowName]
	if from == "" || !mainFrame {
		return
	}
	m, ok := window.ParseFloatMessage(message)
	if !ok {
		return
	}
	m.Window = from
	h.do(func() {
		loaded := h.floatURL
		if from == window.FloatPanel {
			loaded = h.floatPanelURL
		}
		if !sameOrigin(origin, loaded) {
			return
		}
		if on := h.handlers().OnFloat; on != nil {
			on(m)
		}
	})
}

// floatGone tells the controller the web content process of the float's
// window win ended; it runs on the worker.
func (h *host) floatGone(win string) {
	if on := h.handlers().OnFloatGone; on != nil {
		on(win)
	}
}

// setPanelMode keeps mode; a full panel that shows with the pointer already
// off both windows gets the grace the pointer's last move could not start.
func (h *host) setPanelMode(mode string) {
	opened := mode == window.ModePanel && h.panelMode != window.ModePanel
	h.panelMode = mode
	h.panelKeyable.Store(window.FloatKeyable(mode))
	if opened {
		h.grace++
		h.startGrace()
	}
}

// sameOrigin says whether two addresses share their scheme, host and port.
func sameOrigin(a, b string) bool {
	ua, errA := url.Parse(a)
	ub, errB := url.Parse(b)
	return errA == nil && errB == nil && ua.Scheme != "" && ua.Scheme == ub.Scheme && ua.Host == ub.Host
}

// floatMoved starts following a move the person made, a frame other than the
// one the host last set while a mouse button is down, until they let go; a
// move AppKit makes itself, as when a screen goes away, is left to the
// controller's clamp.
func (h *host) floatMoved() {
	if !h.floatShown || h.dragging || h.native.buttonsUp() {
		return
	}
	f := h.native.frame()
	if sameFrame(f, h.floatFrame) {
		return
	}
	h.dragging = true
	if on := h.handlers().OnFloatMoving; on != nil {
		on()
	}
	h.afterRelease(h.dropped)
}

// dropPoll is how often a held button is checked for its release: AppKit
// sends the window no mouse-up for a drag the window server runs.
const dropPoll = 30 * time.Millisecond

// afterRelease runs fn on the worker once no mouse button is down. It is
// called on the worker, which running counts, so its Add never races stop's
// Wait.
func (h *host) afterRelease(fn func()) {
	h.running.Go(func() {
		tick := time.NewTicker(dropPoll)
		defer tick.Stop()
		for {
			select {
			case <-h.done:
				return
			case <-tick.C:
				if h.native.buttonsUp() {
					h.do(fn)
					return
				}
			}
		}
	})
}

func (h *host) dropped() {
	h.dragging = false
	f := h.native.frame()
	h.floatFrame = f
	if on := h.handlers().OnFloatDropped; on != nil {
		on(f)
	}
}

// sameFrame allows the rounding AppKit applies to a frame it was given.
func sameFrame(a, b window.Rect) bool {
	near := func(x, y float64) bool { return math.Abs(x-y) < 1 }
	return near(a.X, b.X) && near(a.Y, b.Y) && near(a.W, b.W) && near(a.H, b.H)
}

func (h *host) screensChanged() {
	var s []window.Screen
	application.InvokeSync(func() { s = screens() })
	if on := h.handlers().OnScreens; on != nil && len(s) > 0 {
		on(s)
	}
}

func (h *host) HidePanel() {
	h.do(func() {
		h.panelShown, h.panelKey = false, false
		h.panel.Hide()
	})
}

// Listen binds the reopen, the float's messages and the screens and, unless
// empty, the hotkey and the float's hotkey; call it after the app launched,
// off the main thread, so a refused chord comes back as the error.
func (h *host) Listen(hs window.Handlers) error {
	h.links.Lock()
	h.mu.Lock()
	h.listening = hs
	h.mu.Unlock()
	if hs.FloatOverFullScreen {
		application.InvokeSync(func() {
			addCollectionBehavior(nsWindow(h.float), application.MacWindowCollectionBehaviorFullScreenAuxiliary)
		})
	}
	h.do(h.screensChanged)
	if hs.OnOpenURL != nil {
		for _, link := range h.pending {
			hs.OnOpenURL(link)
		}
		h.pending = nil
	}
	h.links.Unlock()
	var errs []error
	if hs.Hotkey != "" && hs.OnHotkey != nil {
		errs = append(errs, h.app.GlobalShortcut.Register(hs.Hotkey, hs.OnHotkey))
	}
	if hs.FloatHotkey != "" && hs.OnFloatHotkey != nil {
		errs = append(errs, h.app.GlobalShortcut.Register(hs.FloatHotkey, hs.OnFloatHotkey))
	}
	return errors.Join(errs...)
}

// openURL hands a deep link to the controller, or keeps it until the
// controller listens: a link that launched the app arrives first.
func (h *host) openURL(link string) {
	h.links.Lock()
	defer h.links.Unlock()
	on := h.handlers().OnOpenURL
	if on == nil {
		h.pending = append(h.pending, link)
		return
	}
	on(link)
}

func (h *host) hideMain() {
	h.main.Hide()
	h.mainShown = false
	h.settlePolicy()
}

// panelLeft hides a panel the person left; a focus loss counts only once the
// panel was key since it was shown and is not key now.
func (h *host) panelLeft(closed bool) {
	if !h.panelShown {
		return
	}
	if !closed && (!h.panelKey || isKey(h.panel)) {
		return
	}
	h.panelShown, h.panelKey = false, false
	h.panel.Hide()
	if on := h.handlers().OnPanelHidden; on != nil {
		on()
	}
}

// navigate loads next in w, or only moves its hash on the same page: the
// webview's own URL no longer carries the token once the daemon traded it for
// a cookie, so loading next again would reload the page.
func navigate(w *application.WebviewWindow, current *string, next string) {
	load, hash := window.Navigation(*current, next)
	switch {
	case load:
		w.SetURL(next)
	case hash != "":
		js, _ := json.Marshal("#" + hash)
		evalJS(w, "location.hash = "+string(js))
	default:
		return
	}
	*current = next
}
