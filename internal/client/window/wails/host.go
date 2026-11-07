//go:build darwin && purego

package wails

import (
	"encoding/json"
	"os"
	"runtime"
	"sync"

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
	app := application.New(application.Options{
		Name: name,
		Mac: application.MacOptions{
			ActivationPolicy: application.ActivationPolicyAccessory,
			ApplicationShouldTerminateAfterLastWindowClosed: false,
		},
	})
	app.Menu.Set(wailsMenu(menuOf(name, cat.T)))
	h := newHost(app, name)
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
	_ = app.Run()
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
	app         *application.App
	main, panel *application.WebviewWindow

	mu        sync.Mutex
	queue     []func()
	wake      chan struct{}
	listening window.Handlers

	// Touched only by the worker.
	mainURL, panelURL string
	panelShown        bool
	// panelKey says the panel became key since it was last shown, so a
	// focus loss is the person leaving it, not a late event of an earlier
	// show.
	panelKey bool
}

func newHost(app *application.App, name string) *host {
	h := &host{app: app, wake: make(chan struct{}, 1)}
	h.main = app.Window.NewWithOptions(application.WebviewWindowOptions{
		Name: "main", Title: name, URL: "about:blank", Width: 1280, Height: 820, Hidden: true,
	})
	h.panel = app.Window.NewWithOptions(application.WebviewWindowOptions{
		Name: "panel", Title: name, URL: "about:blank", Width: 680, Height: 420, Hidden: true, Frameless: true,
		InitialPosition: application.WindowCentered,
		Mac: application.MacWindow{
			WindowClass:      application.MacWindowClassPanel,
			PanelPreferences: application.MacPanelPreferences{NonActivating: true, FloatingPanel: true},
			WindowLevel:      application.MacWindowLevelFloating,
			CollectionBehavior: application.MacWindowCollectionBehaviorCanJoinAllSpaces |
				application.MacWindowCollectionBehaviorFullScreenAuxiliary,
		},
	})
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
	go h.work()
	return h
}

// launched restores the main window's frame; it runs on the main thread.
func (h *host) launched() {
	nsWindow(h.main).Send(objc.RegisterName("setFrameAutosaveName:"), nsString(frameAutosaveName))
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
	for range h.wake {
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

func (h *host) ShowMain(url string) {
	h.do(func() {
		navigate(h.main, &h.mainURL, url)
		setActivationPolicy(activationPolicyRegular)
		h.main.Show()
		application.InvokeSync(func() {
			nsApp().Send(objc.RegisterName("activateIgnoringOtherApps:"), true)
			nsWindow(h.main).Send(objc.RegisterName("makeKeyAndOrderFront:"), objc.ID(0))
		})
	})
}

func (h *host) ShowPanel(url string) {
	h.do(func() {
		navigate(h.panel, &h.panelURL, url)
		h.panelShown, h.panelKey = true, false
		h.panel.Show()
		application.InvokeSync(func() { nsWindow(h.panel).Send(objc.RegisterName("makeKeyWindow")) })
	})
}

func (h *host) HidePanel() {
	h.do(func() {
		h.panelShown, h.panelKey = false, false
		h.panel.Hide()
	})
}

// Listen binds the reopen and, unless h.Hotkey is empty, the hotkey; call it
// after the app launched, off the main thread, so a refused chord comes back
// as the error.
func (h *host) Listen(hs window.Handlers) error {
	h.mu.Lock()
	h.listening = hs
	h.mu.Unlock()
	if hs.Hotkey == "" || hs.OnHotkey == nil {
		return nil
	}
	return h.app.GlobalShortcut.Register(hs.Hotkey, hs.OnHotkey)
}

func (h *host) hideMain() {
	h.main.Hide()
	setActivationPolicy(activationPolicyAccessory)
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
