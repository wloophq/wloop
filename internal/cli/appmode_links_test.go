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

	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/client/menubar"
	"github.com/wloophq/wloop/internal/client/texts"
	"github.com/wloophq/wloop/internal/client/window"
	"github.com/wloophq/wloop/internal/config"
)

// linkTray is the menu bar's tray when it runs alone, which the system hands
// the app's opened links.
type linkTray struct {
	recTray
	mu     sync.Mutex
	onLink func(string)
}

func (l *linkTray) OnLink(fn func(string)) { l.mu.Lock(); l.onLink = fn; l.mu.Unlock() }

func (l *linkTray) handler() func(string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.onLink
}

// Without a window (window.enabled = false), an opened link opens the
// browser at its route, and one that cannot be read at the default view,
// which says so.
func TestAnOpenedLinkWithoutAWindowOpensTheBrowser(t *testing.T) {
	t.Parallel()
	d := startDaemon(t, nil)
	te := newTestEnv(t, nil)
	te.Socket = d.socket
	te.Executable = inBundle
	te.NativeAvailable = func() bool { return true }
	if err := os.WriteFile(filepath.Join(te.StateDir, httpInfoFile), []byte(`{"port":4321,"token":"tok"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var opened []string
	te.Sh = func(_ context.Context, name string, args ...string) ([]byte, error) {
		mu.Lock()
		defer mu.Unlock()
		opened = append(opened, name+" "+strings.Join(args, " "))
		return nil, nil
	}
	quit := make(chan struct{})
	te.QuitNative = func() { close(quit) }
	tray := &linkTray{}
	te.RunNative = func(_ config.Window, _ *texts.Catalog, setup func(menubar.Tray, menubar.Notifier, window.Host)) {
		setup(tray, silentNotifier{}, nil)
		open := tray.handler()
		if open == nil {
			t.Error("the menu bar took no links")
		} else {
			open(brand.URLScheme() + "://open/#task=t-1")
			open(brand.URLScheme() + "://elsewhere/#task=t-1")
		}
		want := []string{
			browserCommand() + " http://127.0.0.1:4321/?token=tok#task=t-1",
			browserCommand() + " http://127.0.0.1:4321/?token=tok#" + window.MalformedLinkRoute,
		}
		eventually(t, "the browser to open both links", func() bool {
			mu.Lock()
			defer mu.Unlock()
			return slices.Equal(opened, want)
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
	case <-time.After(10 * time.Second):
		t.Fatal("the app did not stop after Quit")
	}
}
