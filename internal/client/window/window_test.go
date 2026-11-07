package window

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/wloophq/wloop/internal/api"
)

const base = "http://127.0.0.1:7000/?token=a"

func cockpitURL(route string) (string, error) {
	if route == "" {
		return base, nil
	}
	return base + "#" + route, nil
}

// fakeHost records what it was asked, in order.
type fakeHost struct {
	mu       sync.Mutex
	calls    []string
	listened *Handlers
	listen   error
}

func (h *fakeHost) record(s string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.calls = append(h.calls, s)
}

func (h *fakeHost) ShowMain(url string)  { h.record("main " + url) }
func (h *fakeHost) ShowPanel(url string) { h.record("panel " + url) }
func (h *fakeHost) HidePanel()           { h.record("hide-panel") }
func (h *fakeHost) Listen(hs Handlers) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.listened = &hs
	return h.listen
}

func (h *fakeHost) taken() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := h.calls
	h.calls = nil
	return out
}

func (h *fakeHost) handlers() *Handlers {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.listened
}

// fakeEvents serves one stream per call from streams; a nil stream is an
// error. Like the events client, a served stream closes once its source
// closes or the fake stops.
type fakeEvents struct {
	mu      sync.Mutex
	streams []chan api.Change
	clients []string
	at      []time.Time
	stop    chan struct{}
	relays  sync.WaitGroup
}

func (e *fakeEvents) relay(src <-chan api.Change) <-chan api.Change {
	out := make(chan api.Change)
	e.relays.Go(func() {
		defer close(out)
		for {
			select {
			case c, open := <-src:
				if !open {
					return
				}
				select {
				case out <- c:
				case <-e.stop:
					return
				}
			case <-e.stop:
				return
			}
		}
	})
	return out
}

func (e *fakeEvents) Events(ctx context.Context, client string, _ api.Since) (<-chan api.Change, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.clients = append(e.clients, client)
	e.at = append(e.at, time.Now())
	if len(e.streams) == 0 {
		return nil, errors.New("the daemon is down")
	}
	ch := e.streams[0]
	e.streams = e.streams[1:]
	if ch == nil {
		return nil, errors.New("the daemon is down")
	}
	return e.relay(ch), nil
}

func (e *fakeEvents) calls() ([]string, []time.Time) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return slices.Clone(e.clients), slices.Clone(e.at)
}

type logs struct {
	mu    sync.Mutex
	lines []string
}

func (l *logs) logf(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lines = append(l.lines, fmt.Sprintf(format, args...))
}

func (l *logs) all() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return strings.Join(l.lines, "\n")
}

type rig struct {
	host   *fakeHost
	events *fakeEvents
	logs   *logs
	ctl    *Controller
}

func newRig(streams ...chan api.Change) *rig {
	r := &rig{host: &fakeHost{}, events: &fakeEvents{streams: streams, stop: make(chan struct{})}, logs: &logs{}}
	r.ctl = New(Config{Host: r.host, Daemon: r.events, CockpitURL: cockpitURL, Hotkey: "ctrl+option+space", Log: r.logs.logf})
	return r
}

// run starts the controller and stops it, awaited, when the test ends.
func (r *rig) run(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- r.ctl.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		close(r.events.stop)
		<-done
		r.events.relays.Wait()
	})
}

func want(t *testing.T, got []string, want ...string) {
	t.Helper()
	if !slices.Equal(got, want) {
		t.Fatalf("host calls\n got %q\nwant %q", got, want)
	}
}

func TestShowLoadsTheCockpitAtTheRouteAndHidesThePanel(t *testing.T) {
	r := newRig()
	if err := r.ctl.Show("task=t-1"); err != nil {
		t.Fatal(err)
	}
	want(t, r.host.taken(), "main "+base+"#task=t-1", "hide-panel")
}

func TestShowWithAPanelRouteShowsThePanel(t *testing.T) {
	for _, route := range []string{"panel=omnibox", "q=ab&panel=omnibox"} {
		r := newRig()
		if err := r.ctl.Show(route); err != nil {
			t.Fatal(err)
		}
		want(t, r.host.taken(), "panel "+base+"#"+PanelRoute)
	}
}

// A main window loaded on a panel route would run the panel's page, whose
// hand-over of every other route comes back to the main window: the loop
// a panel route must never start.
func TestAPanelRouteNeverLoadsTheMainWindow(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		stream := make(chan api.Change)
		r := newRig(stream)
		r.run(t)
		synctest.Wait()
		stream <- api.Change{Type: "window", Windows: []api.WindowRequest{{Action: api.WindowOpen, Route: "panel=omnibox"}}}
		synctest.Wait()
		for _, c := range r.host.taken() {
			if strings.HasPrefix(c, "main ") {
				t.Fatalf("the main window loaded %q", c)
			}
		}
	})
}

func TestHotkeyTogglesThePanel(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(make(chan api.Change))
		r.run(t)
		synctest.Wait()
		h := r.host.handlers()
		if h == nil || h.Hotkey != "ctrl+option+space" {
			t.Fatalf("listened with %+v", h)
		}
		h.OnHotkey()
		h.OnHotkey()
		want(t, r.host.taken(), "panel "+base+"#"+PanelRoute, "hide-panel")
	})
}

func TestPanelHiddenByTheHostShowsAgainOnTheNextHotkey(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(make(chan api.Change))
		r.run(t)
		synctest.Wait()
		h := r.host.handlers()
		h.OnHotkey()
		h.OnPanelHidden()
		h.OnHotkey()
		want(t, r.host.taken(), "panel "+base+"#"+PanelRoute, "panel "+base+"#"+PanelRoute)
	})
}

func TestPanelOpenShowsTheMainWindowAndHidesThePanel(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		stream := make(chan api.Change)
		r := newRig(stream)
		r.run(t)
		synctest.Wait()
		r.host.handlers().OnHotkey()
		stream <- api.Change{Type: "window", Windows: []api.WindowRequest{{Action: api.WindowOpen, Route: "task=t-1"}}}
		synctest.Wait()
		want(t, r.host.taken(), "panel "+base+"#"+PanelRoute, "main "+base+"#task=t-1", "hide-panel")
		r.host.handlers().OnHotkey()
		want(t, r.host.taken(), "panel "+base+"#"+PanelRoute)
	})
}

func TestPanelRequestShowsThePanel(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		stream := make(chan api.Change)
		r := newRig(stream)
		r.run(t)
		synctest.Wait()
		stream <- api.Change{Type: "window", Windows: []api.WindowRequest{{Action: api.WindowPanel}}}
		synctest.Wait()
		want(t, r.host.taken(), "panel "+base+"#"+PanelRoute)
	})
}

func TestHidePanelRequestHidesThePanel(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		stream := make(chan api.Change)
		r := newRig(stream)
		r.run(t)
		synctest.Wait()
		r.host.handlers().OnHotkey()
		stream <- api.Change{Type: "window", Windows: []api.WindowRequest{{Action: api.WindowHidePanel}}}
		synctest.Wait()
		want(t, r.host.taken(), "panel "+base+"#"+PanelRoute, "hide-panel")
		r.host.handlers().OnHotkey()
		want(t, r.host.taken(), "panel "+base+"#"+PanelRoute)
	})
}

func TestEveryRequestOfAChangeRunsInOrder(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		stream := make(chan api.Change)
		r := newRig(stream)
		r.run(t)
		synctest.Wait()
		r.host.handlers().OnHotkey()
		stream <- api.Change{Type: "window", Windows: []api.WindowRequest{{Action: api.WindowOpen, Route: "task=t-1"}, {Action: api.WindowPanel}}}
		synctest.Wait()
		want(t, r.host.taken(), "panel "+base+"#"+PanelRoute, "main "+base+"#task=t-1", "hide-panel", "panel "+base+"#"+PanelRoute)
	})
}

func TestRunReturnsAfterTheStreamCloses(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		stream := make(chan api.Change)
		r := newRig(stream)
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		go func() { r.ctl.Run(ctx); close(done) }()
		synctest.Wait()
		cancel()
		synctest.Wait()
		select {
		case <-done:
			t.Fatal("Run returned while the events stream was still open")
		default:
		}
		close(stream)
		<-done
	})
}

func TestRowChangesMoveNoWindow(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		stream := make(chan api.Change)
		r := newRig(stream)
		r.run(t)
		synctest.Wait()
		stream <- api.Change{Type: "changed", Epoch: "e", Rev: 3, All: true}
		synctest.Wait()
		want(t, r.host.taken())
	})
}

func TestReopenShowsTheMainWindowWhereItWas(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(make(chan api.Change))
		r.run(t)
		synctest.Wait()
		r.host.handlers().OnReopen()
		want(t, r.host.taken(), "main "+base, "hide-panel")
	})
}

func TestControllerKeepsRunningWhenTheHotkeyFails(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		stream := make(chan api.Change)
		r := newRig(stream)
		r.host.listen = errors.New(`invalid global shortcut "hyper+x"`)
		r.ctl = New(Config{Host: r.host, Daemon: r.events, CockpitURL: cockpitURL, Hotkey: "hyper+x", Log: r.logs.logf})
		r.run(t)
		synctest.Wait()
		if got := r.logs.all(); !strings.Contains(got, "window.hotkey") || !strings.Contains(got, "hyper+x") {
			t.Fatalf("the log %q names neither window.hotkey nor the chord", got)
		}
		stream <- api.Change{Type: "window", Windows: []api.WindowRequest{{Action: api.WindowOpen, Route: "view=needs-me"}}}
		synctest.Wait()
		want(t, r.host.taken(), "main "+base+"#view=needs-me", "hide-panel")
	})
}

func TestControllerSubscribesAsTheWindowClient(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(make(chan api.Change))
		r.run(t)
		synctest.Wait()
		if clients, _ := r.events.calls(); !slices.Equal(clients, []string{api.WindowClient}) {
			t.Fatalf("subscribed as %q", clients)
		}
	})
}

func TestControllerReconnectsAfterTheDaemonGoesAway(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		first, second := make(chan api.Change), make(chan api.Change)
		r := newRig(first, nil, second)
		r.run(t)
		synctest.Wait()
		closed := time.Now()
		close(first)
		time.Sleep(time.Hour)
		synctest.Wait()
		_, at := r.events.calls()
		if len(at) != 3 {
			t.Fatalf("subscribed %d times, want 3", len(at))
		}
		if gap := at[1].Sub(closed); gap != reconnectAfter {
			t.Errorf("resubscribed %v after the stream closed, want %v", gap, reconnectAfter)
		}
		if gap := at[2].Sub(at[1]); gap != reconnectAfter {
			t.Errorf("retried %v after a failed subscribe, want %v", gap, reconnectAfter)
		}
		second <- api.Change{Type: "window", Windows: []api.WindowRequest{{Action: api.WindowOpen, Route: "task=t-2"}}}
		synctest.Wait()
		want(t, r.host.taken(), "main "+base+"#task=t-2", "hide-panel")
	})
}

func TestCockpitURLErrorIsLoggedAndShowsNothing(t *testing.T) {
	r := newRig()
	r.ctl = New(Config{Host: r.host, Daemon: r.events, Log: r.logs.logf,
		CockpitURL: func(string) (string, error) { return "", errors.New("no cockpit address") }})
	if err := r.ctl.Show("task=t-1"); err == nil {
		t.Fatal("Show returned no error")
	}
	r.ctl.TogglePanel()
	want(t, r.host.taken())
	if !strings.Contains(r.logs.all(), "no cockpit address") {
		t.Fatalf("the log %q does not carry the error", r.logs.all())
	}
}
