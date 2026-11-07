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

// appWords are the client strings the daemon serves, for the native menus the app builds once; English when
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
