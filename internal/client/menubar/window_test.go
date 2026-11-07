package menubar

import (
	"context"
	"slices"
	"sync"
	"testing"
	"time"
)

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
