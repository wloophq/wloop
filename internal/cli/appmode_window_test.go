package cli

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/api/client"
	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/client/menubar"
	"github.com/wloophq/wloop/internal/client/window"
	"github.com/wloophq/wloop/internal/config"
)

type recHost struct {
	mu       sync.Mutex
	shown    []string
	listened *window.Handlers
}

func (h *recHost) ShowMain(url string) { h.mu.Lock(); h.shown = append(h.shown, url); h.mu.Unlock() }
func (h *recHost) ShowPanel(string)    {}
func (h *recHost) HidePanel()          {}
func (h *recHost) Listen(hs window.Handlers) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.listened = &hs
	return nil
}

func (h *recHost) state() ([]string, *window.Handlers) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return slices.Clone(h.shown), h.listened
}

func TestAppModeWithAHostRunsTheWindowController(t *testing.T) {
	t.Parallel()
	d := startDaemon(t, nil)
	te := newTestEnv(t, nil)
	te.Socket = d.socket
	te.Executable = inBundle
	te.NativeAvailable = func() bool { return true }
	if err := os.WriteFile(filepath.Join(te.StateDir, httpInfoFile), []byte(`{"port":4321,"token":"tok"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	quit := make(chan struct{})
	te.QuitNative = func() { close(quit) }
	tray := &recTray{}
	host := &recHost{}
	var win config.Window
	te.RunNative = func(w config.Window, setup func(menubar.Tray, menubar.Notifier, window.Host)) {
		win = w
		setup(tray, silentNotifier{}, host)
		eventually(t, "the controller to listen", func() bool {
			_, hs := host.state()
			return hs != nil && hs.Hotkey == "ctrl+option+space"
		})
		eventually(t, "a window request to reach the window", func() bool {
			res, err := client.New(d.socket).Window(context.Background(), api.WindowRequest{Action: api.WindowOpen, Route: "task=t-1"})
			if err != nil || !res.Delivered {
				return false
			}
			shown, _ := host.state()
			return slices.Contains(shown, "http://127.0.0.1:4321/?token=tok#task=t-1")
		})
		eventually(t, "the menu from the daemon", func() bool { return tray.has("Quit " + brand.Name) })
		tray.click(t, "Quit "+brand.Name)
		<-quit
	}
	done := make(chan error, 1)
	go func() { done <- runApp(context.Background(), te.Env, nil) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("the app did not stop after Quit")
	}
	if !win.Enabled {
		t.Errorf("the native loop got the window section %+v", win)
	}
}

type nativeCalls struct {
	mu    sync.Mutex
	calls []string
}

func (n *nativeCalls) add(s string) { n.mu.Lock(); n.calls = append(n.calls, s); n.mu.Unlock() }

func fakeRunners(n *nativeCalls, available bool) nativeRunners {
	return nativeRunners{
		windowAvailable: available,
		runWindow:       func(ready func(window.Host)) { n.add("window"); ready(&recHost{}) },
		attach:          func(setup func(menubar.Tray, menubar.Notifier)) { n.add("attach"); setup(&recTray{}, silentNotifier{}) },
		runMenu:         func(setup func(menubar.Tray, menubar.Notifier)) { n.add("menu"); setup(&recTray{}, silentNotifier{}) },
	}
}

func TestNativeHostRunsTheWindowWhenEnabled(t *testing.T) {
	t.Parallel()
	n := &nativeCalls{}
	var host window.Host
	fakeRunners(n, true).run(config.Window{Enabled: true}, func(_ menubar.Tray, _ menubar.Notifier, h window.Host) { host = h })
	if strings.Join(n.calls, ",") != "window,attach" || host == nil {
		t.Fatalf("ran %v with host %v", n.calls, host)
	}
}

func TestWindowDisabledRunsNoHost(t *testing.T) {
	t.Parallel()
	for name, c := range map[string]struct {
		available, enabled bool
	}{"disabled": {true, false}, "a build without the window": {false, true}} {
		t.Run(name, func(t *testing.T) {
			n := &nativeCalls{}
			hosted := true
			fakeRunners(n, c.available).run(config.Window{Enabled: c.enabled}, func(_ menubar.Tray, _ menubar.Notifier, h window.Host) { hosted = h != nil })
			if strings.Join(n.calls, ",") != "menu" || hosted {
				t.Fatalf("ran %v, host given %v", n.calls, hosted)
			}
		})
	}
}

func TestAppModeWithoutAHostKeepsTheBrowser(t *testing.T) {
	t.Parallel()
	if show := windowShow(context.Background(), nil, nil, nil, config.Window{}, nil); show != nil {
		t.Fatal("no host, yet the menu bar opens a window")
	}
}
