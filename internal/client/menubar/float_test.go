package menubar

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/wloophq/wloop/internal/api"
)

type fakeDefaults struct {
	mu sync.Mutex
	m  map[string]string
}

func (d *fakeDefaults) String(key string) string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.m[key]
}

func (d *fakeDefaults) SetString(key, value string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.m == nil {
		d.m = map[string]string{}
	}
	d.m[key] = value
}

// floatHost counts the float's shows and hides.
type floatHost struct {
	mu           sync.Mutex
	shows, hides int
}

func (h *floatHost) show() error { h.mu.Lock(); h.shows++; h.mu.Unlock(); return nil }
func (h *floatHost) hide()       { h.mu.Lock(); h.hides++; h.mu.Unlock() }

func (h *floatHost) counts() (shows, hides int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.shows, h.hides
}

// floatRig is a menu bar whose float is on, on the clock of the bubble it runs in.
func floatRig(t *testing.T, d *fakeDefaults) (*rig, *floatHost) {
	t.Helper()
	r := &rig{d: newDaemon(), tray: &fakeTray{}, notes: &fakeNotifier{perm: PermissionAuthorized}}
	h := &floatHost{}
	cfg := Config{
		Daemon: r.d, Tray: r.tray, Notifier: r.notes,
		OpenURL:    func(u string) error { return nil },
		CockpitURL: func(route string) (string, error) { return "cockpit#" + route, nil },
		Quit:       func() {},
		Float:      true,
		ShowFloat:  h.show,
		HideFloat:  h.hide,
	}
	if d != nil {
		cfg.Defaults = d
	}
	r.c = New(cfg)
	return r, h
}

func queueOf(n int) *api.Queue {
	q := &api.Queue{Title: "Needs you", View: "needs-you", Total: n}
	for i := range n {
		q.Items = append(q.Items, api.QueueItem{Kind: "answer-question", Task: api.TaskView{ID: fmt.Sprintf("t-%d", i+1), Title: "Publish the review?"}})
	}
	return q
}

// change serves q and tells the menu bar something moved.
func (r *rig) change(q *api.Queue) {
	r.d.mu.Lock()
	r.d.queue = q
	r.d.mu.Unlock()
	r.d.events <- api.Change{All: true}
	synctest.Wait()
}

func (r *rig) mode() string {
	r.c.floatMu.Lock()
	defer r.c.floatMu.Unlock()
	return r.c.vis.Mode
}

func TestQuietShowsOnNextArrivalOnce(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r, h := floatRig(t, nil)
		r.d.events = make(chan api.Change)
		r.d.queue = queueOf(1)
		stop := run(t, r)
		defer stop()
		synctest.Wait()
		r.c.FloatHide(HideQuiet)
		synctest.Wait()
		if shows, hides := h.counts(); shows != 1 || hides != 1 || r.mode() != VisibilityQuiet {
			t.Fatalf("after hiding: shown %d, hidden %d, mode %q", shows, hides, r.mode())
		}
		r.change(queueOf(1))
		if shows, _ := h.counts(); shows != 1 {
			t.Fatalf("a change with nothing new showed the float")
		}
		for n := 2; n <= 21; n++ {
			r.change(queueOf(n))
		}
		if shows, hides := h.counts(); shows != 2 || hides != 1 || r.mode() != VisibilityShown {
			t.Fatalf("20 arrivals: shown %d times, hidden %d, mode %q; want one show", shows, hides, r.mode())
		}
	})
}

func TestSnoozeShowsAtUntil(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r, h := floatRig(t, nil)
		r.d.events = make(chan api.Change)
		stop := run(t, r)
		defer stop()
		synctest.Wait()
		hidAt := time.Now()
		r.c.FloatHide(HideHour)
		synctest.Wait()
		if v := r.c.visibility(); v.Mode != VisibilitySnoozed || !v.Until.Equal(hidAt.Add(time.Hour)) {
			t.Fatalf("visibility %+v, want snoozed until %v", v, hidAt.Add(time.Hour))
		}
		r.change(queueOf(3))
		time.Sleep(time.Until(hidAt.Add(time.Hour)) - time.Nanosecond)
		synctest.Wait()
		if shows, hides := h.counts(); shows != 1 || hides != 1 {
			t.Fatalf("before the hour: shown %d, hidden %d; arrivals must queue silently", shows, hides)
		}
		time.Sleep(time.Nanosecond)
		synctest.Wait()
		if shows, _ := h.counts(); shows != 2 || r.mode() != VisibilityShown {
			t.Fatalf("at the hour: shown %d, mode %q", shows, r.mode())
		}
	})
}

func TestSnoozeUntilTomorrowIsNextLocal0900(t *testing.T) {
	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skipf("no zone database: %v", err)
	}
	for name, c := range map[string]struct {
		now, want time.Time
		wait      time.Duration
	}{
		"just before 09:00":         {time.Date(2026, 11, 25, 8, 59, 0, 0, time.UTC), time.Date(2026, 11, 25, 9, 0, 0, 0, time.UTC), time.Minute},
		"at 09:00":                  {time.Date(2026, 11, 25, 9, 0, 0, 0, time.UTC), time.Date(2026, 11, 26, 9, 0, 0, 0, time.UTC), 24 * time.Hour},
		"just after 09:00":          {time.Date(2026, 11, 25, 9, 1, 0, 0, time.UTC), time.Date(2026, 11, 26, 9, 0, 0, 0, time.UTC), 23*time.Hour + 59*time.Minute},
		"the clocks spring forward": {time.Date(2026, 3, 7, 22, 0, 0, 0, ny), time.Date(2026, 3, 8, 9, 0, 0, 0, ny), 10 * time.Hour},
		"the clocks fall back":      {time.Date(2026, 10, 31, 22, 0, 0, 0, ny), time.Date(2026, 11, 1, 9, 0, 0, 0, ny), 12 * time.Hour},
	} {
		t.Run(name, func(t *testing.T) {
			got := nextMorning(c.now, nine)
			if !got.Equal(c.want) || got.Sub(c.now) != c.wait {
				t.Fatalf("next morning of %v is %v (%v away), want %v (%v away)", c.now, got, got.Sub(c.now), c.want, c.wait)
			}
		})
	}
	synctest.Test(t, func(t *testing.T) {
		r, h := floatRig(t, nil)
		r.d.events = make(chan api.Change)
		at := time.Date(2026, 3, 7, 22, 0, 0, 0, ny)
		start := time.Now()
		r.c.cfg.Now = func() time.Time { return at.Add(time.Since(start)) }
		stop := run(t, r)
		defer stop()
		synctest.Wait()
		r.c.FloatHide(HideTomorrow)
		synctest.Wait()
		if v := r.c.visibility(); v.Mode != VisibilitySnoozed || !v.Until.Equal(time.Date(2026, 3, 8, 9, 0, 0, 0, ny)) {
			t.Fatalf("visibility %+v", v)
		}
		time.Sleep(10*time.Hour - time.Nanosecond)
		synctest.Wait()
		if shows, _ := h.counts(); shows != 1 {
			t.Fatalf("shown %d times before 09:00", shows)
		}
		time.Sleep(time.Nanosecond)
		synctest.Wait()
		if shows, _ := h.counts(); shows != 2 {
			t.Fatalf("shown %d times at 09:00, want the float back", shows)
		}
	})
}

func needsPosted(n *fakeNotifier) int {
	c := 0
	for _, p := range n.snapshot() {
		if strings.HasPrefix(string(p.Category), string(api.CategoryNeeds)) {
			c++
		}
	}
	return c
}

func notesPosted(n *fakeNotifier) int {
	c := 0
	for _, p := range n.snapshot() {
		if p.Category == api.CategoryNote {
			c++
		}
	}
	return c
}

func TestBannersSuppressedUnlessOff(t *testing.T) {
	for _, c := range []struct {
		mode  string
		float bool
		needs int
	}{
		{VisibilityShown, true, 0},
		{VisibilityQuiet, true, 0},
		{VisibilitySnoozed, true, 0},
		{VisibilityOff, true, 1},
		{VisibilityShown, false, 1},
	} {
		t.Run(fmt.Sprintf("%s float %v", c.mode, c.float), func(t *testing.T) {
			r, _ := floatRig(t, nil)
			r.c.cfg.Float = c.float
			r.c.vis, r.c.started = Visibility{Mode: c.mode}, true
			r.d.tasks["inbox"] = []api.TaskView{pendingTask(1)}
			r.c.Refresh(context.Background())
			r.c.postNotice(api.Notice{ID: "n-1", Workflow: "nudge", Text: "a nudge went out"})
			if got := needsPosted(r.notes); got != c.needs {
				t.Errorf("posted %d NEEDS banners, want %d", got, c.needs)
			}
			if got := notesPosted(r.notes); got != 1 {
				t.Errorf("posted %d NOTE banners, want 1", got)
			}
		})
	}
}

func TestShowingTheFloatWithdrawsTheNeedsBanners(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		d := &fakeDefaults{}
		d.SetString(VisibilityKey, `{"mode":"off"}`)
		r, _ := floatRig(t, d)
		r.d.events = make(chan api.Change)
		r.d.tasks["inbox"] = []api.TaskView{pendingTask(1)}
		stop := run(t, r)
		defer stop()
		synctest.Wait()
		if needsPosted(r.notes) != 1 {
			t.Fatalf("off posted %d NEEDS banners", needsPosted(r.notes))
		}
		r.c.ToggleFloat()
		synctest.Wait()
		if w := r.notes.withdrawnIDs(); len(w) != 1 || w[0] != pendingTask(1).Attention.ID {
			t.Fatalf("withdrew %q, want the NEEDS banner the float now carries", w)
		}
	})
}

func TestDigestPostsWhileShown(t *testing.T) {
	r, _ := floatRig(t, nil)
	r.c.vis, r.c.started = Visibility{Mode: VisibilityShown}, true
	day := now.Format(time.DateOnly)
	r.d.digests[day] = api.Digest{Day: day, Title: "Daily digest", Body: "2 new in Next or Waiting", Tasks: []string{"e-1", "e-2"}}
	if err := r.c.postDigest(context.Background(), day); err != nil {
		t.Fatal(err)
	}
	if got := digests(r.notes); len(got) != 1 {
		t.Fatalf("posted %+v, want the digest while the float shows", got)
	}
}

// The menu bar's side of Review Focus 3: the float stays while the daemon is
// down, and a recovery is no arrival.
func TestDaemonDownShowsOfflineThenRecovers(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r, h := floatRig(t, nil)
		r.d.events = make(chan api.Change)
		r.d.queue = queueOf(2)
		stop := run(t, r)
		defer stop()
		synctest.Wait()
		r.c.FloatHide(HideQuiet)
		synctest.Wait()
		r.d.mu.Lock()
		r.d.err = errors.New("connection refused")
		r.d.mu.Unlock()
		r.d.events <- api.Change{All: true}
		synctest.Wait()
		if !strings.HasSuffix(r.title(), "?") {
			t.Fatalf("title %q, want the daemon-down title", r.title())
		}
		if shows, hides := h.counts(); shows != 1 || hides != 1 {
			t.Fatalf("daemon down: shown %d, hidden %d; the float must stay as it was", shows, hides)
		}
		r.d.mu.Lock()
		r.d.err = nil
		r.d.mu.Unlock()
		r.change(queueOf(2))
		if shows, hides := h.counts(); shows != 1 || hides != 1 || r.mode() != VisibilityQuiet {
			t.Fatalf("recovered: shown %d, hidden %d, mode %q; the same queue is no arrival", shows, hides, r.mode())
		}
		r.c.ToggleFloat()
		synctest.Wait()
		r.d.mu.Lock()
		r.d.err = errors.New("connection refused")
		r.d.mu.Unlock()
		r.d.events <- api.Change{All: true}
		synctest.Wait()
		r.d.mu.Lock()
		r.d.err = nil
		r.d.mu.Unlock()
		r.change(queueOf(2))
		if shows, hides := h.counts(); shows != 2 || hides != 1 || r.mode() != VisibilityShown {
			t.Fatalf("shown through an outage: shown %d, hidden %d, mode %q", shows, hides, r.mode())
		}
	})
}

func TestFloatHideModes(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r, h := floatRig(t, nil)
		r.d.events = make(chan api.Change)
		stop := run(t, r)
		defer stop()
		synctest.Wait()
		r.c.FloatHide("sideways")
		synctest.Wait()
		if _, hides := h.counts(); hides != 0 || r.mode() != VisibilityShown {
			t.Fatalf("an unknown mode hid the float (%d) into %q", hides, r.mode())
		}
		for mode, want := range map[string]Visibility{
			HideQuiet:    {Mode: VisibilityQuiet},
			HideHour:     {Mode: VisibilitySnoozed, Until: time.Now().Add(time.Hour)},
			HideTomorrow: {Mode: VisibilitySnoozed, Until: nextMorning(time.Now(), nine)},
		} {
			r.c.ToggleFloat()
			r.c.FloatHide(mode)
			synctest.Wait()
			if got := r.c.visibility(); got.Mode != want.Mode || !got.Until.Equal(want.Until) {
				t.Errorf("float:hide %q: %+v, want %+v", mode, got, want)
			}
		}
	})
}

func TestTheTrayItemTogglesTheFloatOff(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r, h := floatRig(t, nil)
		r.d.events = make(chan api.Change)
		stop := run(t, r)
		defer stop()
		synctest.Wait()
		item := func() MenuItem {
			r.tray.mu.Lock()
			defer r.tray.mu.Unlock()
			it, ok := find(r.tray.menu, "Floating widget")
			if !ok {
				t.Fatalf("no float item in %q", titles(r.tray.menu))
			}
			return it
		}
		if it := item(); !it.Checked || !it.Enabled {
			t.Fatalf("shown, the item is %+v; want it checked", it)
		}
		item().Action()
		synctest.Wait()
		if _, hides := h.counts(); hides != 1 || r.mode() != VisibilityOff || item().Checked {
			t.Fatalf("after the click: hidden %d, mode %q, checked %v", hides, r.mode(), item().Checked)
		}
		item().Action()
		synctest.Wait()
		if shows, _ := h.counts(); shows != 2 || r.mode() != VisibilityShown || !item().Checked {
			t.Fatalf("after the second click: shown %d, mode %q, checked %v", shows, r.mode(), item().Checked)
		}
		r.c.FloatHide(HideHour)
		synctest.Wait()
		if !item().Checked {
			t.Fatal("snoozed, the float is still on; the item must stay checked")
		}
		item().Action()
		synctest.Wait()
		if shows, _ := h.counts(); shows != 3 || r.mode() != VisibilityShown {
			t.Fatalf("the item did not bring a snoozed float back: shown %d, mode %q", shows, r.mode())
		}
	})
}

func TestTheFloatItemIsOnlyThereWhenTheFloatIsOn(t *testing.T) {
	r := newRig(t)
	r.c.Refresh(context.Background())
	if _, ok := find(r.tray.menu, "Floating widget"); ok {
		t.Fatalf("float off, yet the menu offers it: %q", titles(r.tray.menu))
	}
}

func TestTheHotkeyTogglesShownAndQuiet(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r, h := floatRig(t, nil)
		r.d.events = make(chan api.Change)
		stop := run(t, r)
		defer stop()
		synctest.Wait()
		r.c.ToggleFloat()
		synctest.Wait()
		if _, hides := h.counts(); hides != 1 || r.mode() != VisibilityQuiet {
			t.Fatalf("hotkey on a shown float: hidden %d, mode %q", hides, r.mode())
		}
		r.c.ToggleFloat()
		synctest.Wait()
		if shows, _ := h.counts(); shows != 2 || r.mode() != VisibilityShown {
			t.Fatalf("hotkey on a quiet float: shown %d, mode %q", shows, r.mode())
		}
	})
}

func TestARelaunchKeepsASnooze(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		d := &fakeDefaults{}
		r, _ := floatRig(t, d)
		r.d.events = make(chan api.Change)
		stop := run(t, r)
		synctest.Wait()
		hidAt := time.Now()
		r.c.FloatHide(HideHour)
		synctest.Wait()
		stop()
		if d.String(VisibilityKey) == "" {
			t.Fatalf("nothing saved under %q", VisibilityKey)
		}

		r2, h2 := floatRig(t, d)
		r2.d.events = make(chan api.Change)
		stop2 := run(t, r2)
		defer stop2()
		synctest.Wait()
		if shows, _ := h2.counts(); shows != 0 || r2.mode() != VisibilitySnoozed {
			t.Fatalf("relaunched: shown %d, mode %q; want the snooze kept", shows, r2.mode())
		}
		time.Sleep(time.Until(hidAt.Add(time.Hour)))
		synctest.Wait()
		if shows, _ := h2.counts(); shows != 1 {
			t.Fatalf("relaunched: shown %d at the snooze's end", shows)
		}
	})
}

func TestARelaunchPastTheSnoozeShows(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		d := &fakeDefaults{}
		d.SetString(VisibilityKey, fmt.Sprintf(`{"mode":"snoozed","until":%q}`, time.Now().Add(-time.Minute).Format(time.RFC3339Nano)))
		r, h := floatRig(t, d)
		r.d.events = make(chan api.Change)
		stop := run(t, r)
		defer stop()
		synctest.Wait()
		if shows, _ := h.counts(); shows != 1 || r.mode() != VisibilityShown {
			t.Fatalf("an expired snooze: shown %d, mode %q", shows, r.mode())
		}
	})
}

func TestARelaunchKeepsTheFloatOff(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		d := &fakeDefaults{}
		d.SetString(VisibilityKey, `{"mode":"off"}`)
		r, h := floatRig(t, d)
		r.d.events = make(chan api.Change)
		r.d.tasks["inbox"] = []api.TaskView{pendingTask(1)}
		stop := run(t, r)
		defer stop()
		synctest.Wait()
		if shows, _ := h.counts(); shows != 0 || needsPosted(r.notes) != 1 {
			t.Fatalf("off at relaunch: shown %d, NEEDS banners %d", shows, needsPosted(r.notes))
		}
	})
}

var nine = clockTime{9, 0}

func TestTheMorningIsTheConfiguredTime(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r, _ := floatRig(t, nil)
		r.c.cfg.FloatMorning = "07:30"
		r.d.events = make(chan api.Change)
		at := time.Date(2026, 11, 25, 22, 0, 0, 0, time.UTC)
		r.c.cfg.Now = func() time.Time { return at }
		stop := run(t, r)
		defer stop()
		synctest.Wait()
		r.c.FloatHide(HideTomorrow)
		synctest.Wait()
		if v := r.c.visibility(); !v.Until.Equal(time.Date(2026, 11, 26, 7, 30, 0, 0, time.UTC)) {
			t.Fatalf("until %v, want 07:30 tomorrow", v.Until)
		}
	})
}

func TestAnUnreadableMorningIsNine(t *testing.T) {
	r, _ := floatRig(t, nil)
	r.c.cfg.FloatMorning = "9am"
	if got := r.c.morning(); got != nine {
		t.Fatalf("morning %v, want 09:00", got)
	}
}

// Items that arrived while the app was closed bring a quiet float back.
func TestARelaunchInQuietComparesWithTheSavedQueue(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		d := &fakeDefaults{}
		r, _ := floatRig(t, d)
		r.d.events = make(chan api.Change)
		r.d.queue = queueOf(1)
		stop := run(t, r)
		synctest.Wait()
		r.c.FloatHide(HideQuiet)
		synctest.Wait()
		stop()

		r2, h2 := floatRig(t, d)
		r2.d.events = make(chan api.Change)
		r2.d.queue = queueOf(2)
		stop2 := run(t, r2)
		defer stop2()
		synctest.Wait()
		r2.change(queueOf(2))
		if shows, _ := h2.counts(); shows != 1 || r2.mode() != VisibilityShown {
			t.Fatalf("relaunched quiet with t-2 new: shown %d, mode %q; want one show", shows, r2.mode())
		}
	})
}

// While quiet, each read keeps the queue the user last saw: an item that left
// and came back while the app was closed is new.
func TestQuietKeepsTheLastReadQueue(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		d := &fakeDefaults{}
		r, _ := floatRig(t, d)
		r.d.events = make(chan api.Change)
		r.d.queue = queueOf(2)
		stop := run(t, r)
		synctest.Wait()
		r.c.FloatHide(HideQuiet)
		synctest.Wait()
		r.change(queueOf(1))
		stop()

		r2, h2 := floatRig(t, d)
		r2.d.events = make(chan api.Change)
		r2.d.queue = queueOf(2)
		stop2 := run(t, r2)
		defer stop2()
		synctest.Wait()
		if shows, _ := h2.counts(); shows != 1 {
			t.Fatalf("shown %d times; t-2 left and came back, want one show", shows)
		}
	})
}

// A snooze whose timer stood still, as on a sleeping Mac, ends on the next
// refresh past its time.
func TestASnoozeThatSleptPastUntilShowsOnTheNextRefresh(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r, h := floatRig(t, nil)
		r.d.events = make(chan api.Change)
		var mu sync.Mutex
		at := time.Date(2026, 11, 25, 10, 0, 0, 0, time.UTC)
		r.c.cfg.Now = func() time.Time { mu.Lock(); defer mu.Unlock(); return at }
		r.c.cfg.After = func(time.Duration) <-chan time.Time { return make(chan time.Time) }
		stop := run(t, r)
		defer stop()
		synctest.Wait()
		r.c.FloatHide(HideHour)
		synctest.Wait()
		mu.Lock()
		at = at.Add(3 * time.Hour)
		mu.Unlock()
		r.d.events <- api.Change{Health: true}
		synctest.Wait()
		if shows, _ := h.counts(); shows != 2 || r.mode() != VisibilityShown {
			t.Fatalf("after waking past the snooze: shown %d, mode %q", shows, r.mode())
		}
	})
}

func TestNothingMovesTheFloatOnceRunEnded(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r, h := floatRig(t, nil)
		r.d.events = make(chan api.Change)
		stop := run(t, r)
		synctest.Wait()
		stop()
		r.c.FloatHide(HideHour)
		r.c.ToggleFloat()
		synctest.Wait()
		if shows, hides := h.counts(); shows != 1 || hides != 0 || r.mode() != VisibilityShown {
			t.Fatalf("after Run ended: shown %d, hidden %d, mode %q", shows, hides, r.mode())
		}
	})
}

func TestNothingMovesTheFloatBeforeItsVisibilityLoaded(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		d := &fakeDefaults{}
		d.SetString(VisibilityKey, `{"mode":"off"}`)
		r, h := floatRig(t, d)
		r.c.FloatHide(HideQuiet)
		r.c.ToggleFloat()
		r.c.trayFloat()
		synctest.Wait()
		if shows, hides := h.counts(); shows != 0 || hides != 0 || r.mode() != "" {
			t.Fatalf("before the load: shown %d, hidden %d, mode %q", shows, hides, r.mode())
		}
		if d.String(VisibilityKey) != `{"mode":"off"}` {
			t.Fatalf("the saved visibility became %s", d.String(VisibilityKey))
		}
	})
}

// With the daemon down at launch the float shows before Run, so its page can
// say it is offline.
func TestTheFloatShowsWhileTheDaemonIsDownAtLaunch(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r, h := floatRig(t, nil)
		r.d.err = errors.New("connection refused")
		r.c.StartFloat()
		r.c.Refresh(context.Background())
		r.c.StartFloat()
		synctest.Wait()
		if shows, hides := h.counts(); shows != 1 || hides != 0 || r.mode() != VisibilityShown {
			t.Fatalf("daemon down at launch: shown %d, hidden %d, mode %q", shows, hides, r.mode())
		}
		r.c.ToggleFloat()
		if _, hides := h.counts(); hides != 1 {
			t.Fatalf("the hotkey did nothing while the daemon was down: hidden %d", hides)
		}
	})
}
