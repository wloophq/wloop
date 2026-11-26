package menubar

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/client/window"
)

type linkTray struct {
	fakeTray
	onLink func(string)
}

func (l *linkTray) OnLink(fn func(string)) { l.onLink = fn }

func TestATrayThatTakesLinksOpensThemInTheBrowser(t *testing.T) {
	tray := &linkTray{}
	var mu sync.Mutex
	var opened []string
	var logged []string
	New(Config{
		Daemon: newDaemon(), Tray: tray, Notifier: &fakeNotifier{perm: PermissionAuthorized},
		OpenURL:    func(u string) error { mu.Lock(); opened = append(opened, u); mu.Unlock(); return nil },
		CockpitURL: func(route string) (string, error) { return "cockpit#" + route, nil },
		Quit:       func() {},
		Now:        func() time.Time { return now },
		Log:        func(f string, a ...any) { logged = append(logged, fmt.Sprintf(f, a...)) },
	})
	if tray.onLink == nil {
		t.Fatal("the controller took no links from the tray")
	}
	tray.onLink(brand.URLScheme() + "://open/#view=inbox")
	tray.onLink(brand.URLScheme() + "://other/#view=inbox")
	if want := []string{"cockpit#view=inbox", "cockpit#" + window.MalformedLinkRoute}; !slices.Equal(opened, want) {
		t.Fatalf("opened %q, want %q", opened, want)
	}
	if len(logged) != 1 || !strings.Contains(logged[0], brand.URLScheme()+"://other/#view=inbox") {
		t.Errorf("logged %q", logged)
	}
}

func TestNotificationOpenFocusesTheWindow(t *testing.T) {
	r := &rig{d: newDaemon(), tray: &fakeTray{}, notes: &fakeNotifier{perm: PermissionAuthorized}}
	var mu sync.Mutex
	var shown []string
	r.c = New(Config{
		Daemon: r.d, Tray: r.tray, Notifier: r.notes,
		OpenURL:    func(u string) error { r.mu.Lock(); r.opened = append(r.opened, u); r.mu.Unlock(); return nil },
		CockpitURL: func(route string) (string, error) { return "cockpit#" + route, nil },
		Window:     func(route string) error { mu.Lock(); shown = append(shown, route); mu.Unlock(); return nil },
		Quit:       func() {},
		Now:        func() time.Time { return now },
	})
	r.c.Respond(context.Background(), Response{ID: pendingTask(1).Attention.ID, Action: ActionOpen})
	waitFor(t, func() bool { mu.Lock(); defer mu.Unlock(); return slices.Equal(shown, []string{"task=e-1"}) })
	if o := r.openedURLs(); len(o) != 0 {
		t.Fatalf("the browser opened %q", o)
	}
}

func TestOpenCockpitShowsTheHomeInTheWindow(t *testing.T) {
	r := &rig{d: newDaemon(), tray: &fakeTray{}, notes: &fakeNotifier{perm: PermissionAuthorized}}
	var mu sync.Mutex
	var shown []string
	r.c = New(Config{
		Daemon: r.d, Tray: r.tray, Notifier: r.notes,
		OpenURL:    func(u string) error { r.mu.Lock(); r.opened = append(r.opened, u); r.mu.Unlock(); return nil },
		CockpitURL: func(route string) (string, error) { return "cockpit#" + route, nil },
		Window:     func(route string) error { mu.Lock(); shown = append(shown, route); mu.Unlock(); return nil },
		Quit:       func() {},
		Now:        func() time.Time { return now },
	})
	r.c.Refresh(context.Background())
	it, ok := find(r.tray.menu, "Open cockpit")
	if !ok {
		t.Fatalf("menu %q", titles(r.tray.menu))
	}
	it.Action()
	waitFor(t, func() bool { mu.Lock(); defer mu.Unlock(); return slices.Equal(shown, []string{window.HomeRoute}) })
}

// The floating widget shows at launch only when it is on; it is off unless
// the configuration turns it on.
func TestTheFloatShowsAtLaunchOnlyWhenOn(t *testing.T) {
	for _, on := range []bool{false, true} {
		r := newRig(t)
		var mu sync.Mutex
		shown := 0
		r.c.cfg.Float = on
		r.c.cfg.ShowFloat = func() error { mu.Lock(); shown++; mu.Unlock(); return nil }
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() { done <- r.c.Run(ctx) }()
		waitFor(t, func() bool { r.d.mu.Lock(); defer r.d.mu.Unlock(); return len(r.d.app) > 0 })
		cancel()
		<-done
		mu.Lock()
		got := shown
		mu.Unlock()
		if want := map[bool]int{false: 0, true: 1}[on]; got != want {
			t.Errorf("float on %v: shown %d times, want %d", on, got, want)
		}
	}
}
