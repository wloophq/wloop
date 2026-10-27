package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/api/client"
	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/client/menubar"
	"github.com/wloophq/wloop/internal/core/lock"
)

var inBundle = "/Users/me/Applications/" + brand.App + ".app/Contents/MacOS/" + brand.Name

func TestAppQuitExitsZero(t *testing.T) {
	t.Parallel()
	d := startDaemon(t, nil)
	te := newTestEnv(t, nil)
	te.Socket = d.socket
	te.Executable = inBundle
	te.NativeAvailable = func() bool { return true }
	quit := make(chan struct{})
	te.QuitNative = func() { close(quit) }
	tray := &recTray{}
	te.RunNative = func(setup func(menubar.Tray, menubar.Notifier)) {
		setup(tray, silentNotifier{})
		eventually(t, "the menu from the daemon", func() bool { return tray.title() != "PR ?" && tray.has("Quit "+brand.Name) })
		eventually(t, "the daemon to see the notifier", func() bool {
			h, err := client.New(d.socket).Health(context.Background())
			return err == nil && h.Notifier
		})
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
	case <-time.After(10 * time.Second):
		t.Fatal("the app did not stop after Quit")
	}
	for _, r := range d.seen() {
		if !strings.HasPrefix(r, "GET ") && r != "POST /api/app" {
			t.Errorf("the app sent %s", r)
		}
	}
	if !slices.Contains(d.seen(), "GET /api/events") {
		t.Errorf("the app never followed the daemon: %v", d.seen())
	}
}

func TestAppShowsTheDaemonMissingWhileItWaits(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, nil)
	te.Socket = shortDir(t) + "/none.sock"
	te.Executable = inBundle
	te.NativeAvailable = func() bool { return true }
	quit := make(chan struct{})
	te.QuitNative = func() { close(quit) }
	tray := &recTray{}
	te.RunNative = func(setup func(menubar.Tray, menubar.Notifier)) {
		setup(tray, silentNotifier{})
		eventually(t, "the menu without the daemon", func() bool { return tray.title() == "PR ?" && tray.has("Quit "+brand.Name) })
		tray.click(t, "Quit "+brand.Name)
		<-quit
	}
	if err := runApp(context.Background(), te.Env, nil); err != nil {
		t.Fatal(err)
	}
}

func TestAppOutsideTheBundleIsAnError(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, nil)
	te.Executable = "/opt/homebrew/bin/" + brand.Name
	te.NativeAvailable = func() bool { return true }
	te.RunNative = func(func(menubar.Tray, menubar.Notifier)) { t.Error("the menu ran outside the bundle") }
	if err := runApp(context.Background(), te.Env, nil); err == nil || !strings.Contains(err.Error(), brand.Command+" app install") {
		t.Fatalf("err = %v", err)
	}
}

func TestAppOffMacOSIsAnError(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, nil)
	te.Executable = inBundle
	if err := runApp(context.Background(), te.Env, nil); !errors.Is(err, errNotMacOS) {
		t.Fatalf("err = %v", err)
	}
}

type recTray struct {
	mu    sync.Mutex
	t     string
	items []menubar.MenuItem
}

func (r *recTray) SetTitle(s string)                { r.mu.Lock(); r.t = s; r.mu.Unlock() }
func (r *recTray) SetMenu(items []menubar.MenuItem) { r.mu.Lock(); r.items = items; r.mu.Unlock() }

func (r *recTray) title() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.t
}

func (r *recTray) has(title string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.ContainsFunc(r.items, func(it menubar.MenuItem) bool { return it.Title == title })
}

func (r *recTray) click(t *testing.T, title string) {
	t.Helper()
	r.mu.Lock()
	var action func()
	for _, it := range r.items {
		if it.Title == title {
			action = it.Action
		}
	}
	r.mu.Unlock()
	if action == nil {
		t.Fatalf("no menu item %q", title)
	}
	action()
}

type silentNotifier struct{}

func (silentNotifier) Authorize(context.Context) (menubar.Permission, error) {
	return menubar.PermissionAuthorized, nil
}
func (silentNotifier) Permission(context.Context) (menubar.Permission, error) {
	return menubar.PermissionAuthorized, nil
}
func (silentNotifier) Post(menubar.Notification) error   { return nil }
func (silentNotifier) Withdraw([]string)                 {}
func (silentNotifier) WithdrawAll()                      {}
func (silentNotifier) OnResponse(func(menubar.Response)) {}

func TestRunInTheBundleWithoutArgumentsStartsTheApp(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{nil, {"-psn_0_12345"}} {
		te := newTestEnv(t, nil)
		te.Executable = inBundle
		if code := te.run(args...); code == 0 || !strings.Contains(te.err.String(), errNotMacOS.Error()) {
			t.Errorf("%q: exit %d: %s", args, code, te.err)
		}
	}
	te := newTestEnv(t, nil)
	te.Executable = "/opt/homebrew/bin/" + brand.Name
	if code := te.run(); code != 0 || !strings.Contains(te.out.String(), "app install") {
		t.Fatalf("exit %d: %s%s", code, te.out, te.err)
	}
}

func TestNotifierStartKickstartsTheAppAgent(t *testing.T) {
	old := goos
	t.Cleanup(func() { goos = old })
	goos = "darwin"
	want := fmt.Sprintf("launchctl kickstart gui/%d/"+brand.LaunchAgent()+".app", os.Getuid())
	if got := notifierStart(); got != want {
		t.Fatalf("notifierStart = %q, want %q", got, want)
	}
	te := newTestEnv(t, nil)
	te.cfg = withoutWindows(te.cfg)
	if d := te.serveDeps(context.Background(), nil, te.cfg, func(string, ...any) {}, &lock.Mutex{}, nil); d.NotifierStart != want {
		t.Fatalf("serve deps NotifierStart = %q", d.NotifierStart)
	}
	goos = "linux"
	if got := notifierStart(); got != "" {
		t.Fatalf("off macOS: %q", got)
	}
}
