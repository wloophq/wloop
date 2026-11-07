package cli

import (
	"context"
	"sync"

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
	runWindow       func(ready func(window.Host))
	attach          func(setup func(menubar.Tray, menubar.Notifier))
	runMenu         func(setup func(menubar.Tray, menubar.Notifier))
}

func macNative(imageDir string) nativeRunners {
	return nativeRunners{
		windowAvailable: wails.Available(),
		runWindow:       func(ready func(window.Host)) { wails.Run(brand.Name, texts.English(), ready) },
		attach:          func(setup func(menubar.Tray, menubar.Notifier)) { macos.Attach(imageDir, setup) },
		runMenu:         func(setup func(menubar.Tray, menubar.Notifier)) { macos.Run(imageDir, setup) },
	}
}

// run runs the window host when this build has one and win enables it, the
// menu bar alone otherwise, whose setup gets no host.
func (n nativeRunners) run(win config.Window, setup func(menubar.Tray, menubar.Notifier, window.Host)) {
	if n.windowAvailable && win.Enabled {
		n.runWindow(func(h window.Host) {
			n.attach(func(t menubar.Tray, notes menubar.Notifier) { setup(t, notes, h) })
		})
		return
	}
	n.runMenu(func(t menubar.Tray, notes menubar.Notifier) { setup(t, notes, nil) })
}

// windowShow starts the window controller on host until ctx ends, in wg, and
// returns how the menu bar shows a route in the window; nil without a host.
func windowShow(ctx context.Context, wg *sync.WaitGroup, host window.Host, daemon window.Events, cockpit func(string) (string, error), win config.Window, logf func(string, ...any)) func(string) error {
	if host == nil {
		return nil
	}
	ctl := window.New(window.Config{Host: host, Daemon: daemon, CockpitURL: cockpit, Hotkey: win.Hotkey, Log: logf})
	wg.Go(func() { _ = ctl.Run(ctx) })
	return ctl.Show
}
