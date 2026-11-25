package cli

import (
	"context"
	"sync"
	"time"

	"github.com/wloophq/wloop/internal/api"

	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/client/menubar"
	"github.com/wloophq/wloop/internal/client/menubar/macos"
	"github.com/wloophq/wloop/internal/client/texts"
	"github.com/wloophq/wloop/internal/client/window"
	"github.com/wloophq/wloop/internal/client/window/wails"
	"github.com/wloophq/wloop/internal/config"
)

// nativeRunners are the app's two native loops: the window host, which the
// menu bar attaches to, and the menu bar alone.
type nativeRunners struct {
	windowAvailable bool
	runWindow       func(words *texts.Catalog, ready func(window.Host))
	attach          func(setup func(menubar.Tray, menubar.Notifier))
	runMenu         func(setup func(menubar.Tray, menubar.Notifier))
}

func macNative(imageDir string) nativeRunners {
	return nativeRunners{
		windowAvailable: wails.Available(),
		runWindow:       func(words *texts.Catalog, ready func(window.Host)) { wails.Run(brand.Name, words, ready) },
		attach:          func(setup func(menubar.Tray, menubar.Notifier)) { macos.Attach(imageDir, setup) },
		runMenu:         func(setup func(menubar.Tray, menubar.Notifier)) { macos.Run(imageDir, setup) },
	}
}

// run runs the window host, its menus worded by words, when this build has one
// and win enables it, the menu bar alone otherwise, whose setup gets no host.
func (n nativeRunners) run(win config.Window, words *texts.Catalog, setup func(menubar.Tray, menubar.Notifier, window.Host)) {
	if n.windowAvailable && win.Enabled {
		n.runWindow(words, func(h window.Host) {
			n.attach(func(t menubar.Tray, notes menubar.Notifier) { setup(t, notes, h) })
		})
		return
	}
	n.runMenu(func(t menubar.Tray, notes menubar.Notifier) { setup(t, notes, nil) })
}

// appWords are the client strings the daemon serves, for the native menus the app builds at its start; English when
// the daemon does not answer within a second.
func appWords(ctx context.Context, daemon interface {
	Strings(context.Context) (api.Strings, error)
}) *texts.Catalog {
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	s, err := daemon.Strings(ctx)
	if err != nil {
		return texts.English()
	}
	return texts.New(s)
}

// rewordOf is how host words its own menus anew from the client strings; nil when it has none.
func rewordOf(host window.Host) func(*texts.Catalog) {
	if r, ok := host.(interface{ Reword(*texts.Catalog) }); ok {
		return r.Reword
	}
	return nil
}

// windowShow starts the window controller on host until ctx ends, in wg; nil
// without a host. A host that keeps user defaults keeps the widget's
// placement in them; the widget's hotkey is bound only while win turns it on.
func windowShow(ctx context.Context, wg *sync.WaitGroup, host window.Host, daemon window.Events, cockpit func(string) (string, error), win config.Window, logf func(string, ...any)) *window.Controller {
	if host == nil {
		return nil
	}
	defaults, _ := host.(window.Defaults)
	cfg := window.Config{Host: host, Daemon: daemon, CockpitURL: cockpit, Hotkey: win.Hotkey, Log: logf, Defaults: defaults}
	if win.Float == config.FloatOn {
		cfg.FloatHotkey, cfg.FloatOverFullScreen = win.FloatHotkey, win.FloatOverFullScreen
	}
	ctl := window.New(cfg)
	wg.Go(func() { _ = ctl.Run(ctx) })
	return ctl
}

// withWindow is cfg showing routes in ctl's window and, when win turns it
// on, the floating widget, whose visibility host's user defaults keep; cfg
// as it is without a controller.
func withWindow(cfg menubar.Config, ctl *window.Controller, host window.Host, win config.Window) menubar.Config {
	if ctl == nil {
		return cfg
	}
	cfg.Window, cfg.ShowFloat, cfg.HideFloat, cfg.FloatReady = ctl.Show, ctl.ShowFloat, ctl.HideFloat, ctl.FloatReady
	cfg.Float, cfg.FloatMorning = win.Float == config.FloatOn, win.FloatMorning
	cfg.Defaults, _ = host.(window.Defaults)
	return cfg
}

// hookFloat hands the hides the float's page asks for and its hotkey to the
// menu bar, which decides what the float shows.
func hookFloat(ctl *window.Controller, c *menubar.Controller) {
	if ctl == nil {
		return
	}
	ctl.OnFloatHide(c.FloatHide)
	ctl.OnFloatHotkey(c.ToggleFloat)
	ctl.OnFloatReady(c.FloatPageReady)
}
