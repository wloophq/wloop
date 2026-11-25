package window

import (
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/brand"
)

// A laptop screen with its menu bar, and a larger screen to its right with
// its own menu bar and the Dock, in the coordinates of a Rect.
var (
	laptop = Screen{Frame: Rect{X: 0, Y: 0, W: 1512, H: 944}, Visible: Rect{X: 0, Y: 33, W: 1512, H: 911}}
	wide   = Screen{Frame: Rect{X: 1512, Y: 0, W: 2560, H: 1440}, Visible: Rect{X: 1512, Y: 25, W: 2560, H: 1390}}
)

func wantRect(t *testing.T, got, want Rect) {
	t.Helper()
	if got != want {
		t.Fatalf("frame %+v, want %+v", got, want)
	}
}

// A pill left on a screen that was unplugged comes back on the main screen,
// inside its visible frame.
func TestClampToScreensFallsBackToMainWhenScreenGone(t *testing.T) {
	onWide := FloatPlacement{Corner: CornerBottomRight, X: 3900, Y: 1300, W: 112, H: 32}
	wantRect(t, ClampToScreens(onWide, []Screen{laptop, wide}), Rect{X: 3900 - 112, Y: 1300 - 32, W: 112, H: 32})
	wantRect(t, ClampToScreens(onWide, []Screen{laptop}), Rect{X: 1512 - 112, Y: 33 + 911 - 32, W: 112, H: 32})
}

// A docked pill stays flush with its edge, wherever its saved point drifted,
// and inside the screen's height.
func TestClampToScreensKeepsEdgeDock(t *testing.T) {
	right := FloatPlacement{Edge: EdgeRight, Corner: CornerTopRight, X: 4050, Y: 300, W: 34, H: 84}
	wantRect(t, ClampToScreens(right, []Screen{laptop, wide}), Rect{X: 1512 + 2560 - 34, Y: 300, W: 34, H: 84})

	left := FloatPlacement{Edge: EdgeLeft, Corner: CornerBottomLeft, X: 40, Y: 2000, W: 34, H: 84}
	wantRect(t, ClampToScreens(left, []Screen{laptop, wide}), Rect{X: 0, Y: 944 - 84, W: 34, H: 84})
}

// A frame larger than its screen's visible frame shrinks to it.
func TestClampToScreensCapsTheSizeToTheVisibleFrame(t *testing.T) {
	big := FloatPlacement{Corner: CornerBottomRight, X: 1500, Y: 900, W: 2000, H: 1200}
	wantRect(t, ClampToScreens(big, []Screen{laptop}), laptop.Visible)
}

// A pill dropped in the second screen's menu-bar strip, outside every
// visible frame, stays on that screen, under its menu bar.
func TestAPillDroppedInAMenuBarStripStaysOnItsScreen(t *testing.T) {
	screens := []Screen{laptop, wide}
	p := PlacementOf(Rect{X: 2000, Y: 5, W: 112, H: 32}, screens)
	if want := (FloatPlacement{Corner: CornerTopLeft, X: 2000, Y: 5, W: 112, H: 32}); p != want {
		t.Fatalf("placement %+v, want %+v", p, want)
	}
	wantRect(t, ClampToScreens(p, screens), Rect{X: 2000, Y: 25, W: 112, H: 32})
}

// A frame across two screens belongs to the one holding more of it.
func TestAFrameAcrossScreensBelongsToTheOneHoldingMore(t *testing.T) {
	screens := []Screen{laptop, wide}
	p := PlacementOf(Rect{X: 1430, Y: 600, W: 112, H: 32}, screens)
	if p.Edge != EdgeRight || p.X != 1512 {
		t.Fatalf("placement %+v, want docked on the laptop's right edge", p)
	}
}

func TestPlacementOfAMovedFrame(t *testing.T) {
	screens := []Screen{laptop, wide}
	cases := []struct {
		name  string
		frame Rect
		want  FloatPlacement
	}{
		{"free in the bottom right", Rect{X: 1200, Y: 800, W: 112, H: 32}, FloatPlacement{Corner: CornerBottomRight, X: 1312, Y: 832, W: 112, H: 32}},
		{"free in the top left", Rect{X: 100, Y: 100, W: 112, H: 32}, FloatPlacement{Corner: CornerTopLeft, X: 100, Y: 100, W: 112, H: 32}},
		{"near the left edge", Rect{X: 6, Y: 700, W: 34, H: 84}, FloatPlacement{Edge: EdgeLeft, Corner: CornerBottomLeft, X: 0, Y: 784, W: 34, H: 84}},
		{"near the second screen's right edge", Rect{X: 4030, Y: 200, W: 34, H: 84}, FloatPlacement{Edge: EdgeRight, Corner: CornerTopRight, X: 4072, Y: 200, W: 34, H: 84}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := PlacementOf(c.frame, screens); got != c.want {
				t.Fatalf("placement %+v, want %+v", got, c.want)
			}
		})
	}
}

// What a moved frame becomes, clamped back, is where it was dropped, or
// flush with the edge it was dropped near.
func TestAMovedFrameClampsToWhereItWasDropped(t *testing.T) {
	screens := []Screen{laptop, wide}
	free := Rect{X: 2000, Y: 900, W: 112, H: 32}
	wantRect(t, ClampToScreens(PlacementOf(free, screens), screens), free)
	nearLeft := Rect{X: 9, Y: 400, W: 34, H: 84}
	wantRect(t, ClampToScreens(PlacementOf(nearLeft, screens), screens), Rect{X: 0, Y: 400, W: 34, H: 84})
}

type memDefaults map[string]string

func (m memDefaults) String(key string) string    { return m[key] }
func (m memDefaults) SetString(key, value string) { m[key] = value }

func TestPlacementSavesAndLoadsUnderTheBrandsDomain(t *testing.T) {
	d := memDefaults{}
	if _, ok := LoadPlacement(d); ok {
		t.Fatal("loaded a placement from empty defaults")
	}
	p := FloatPlacement{Edge: EdgeRight, Corner: CornerTopRight, X: 4072, Y: 200, W: 34, H: 84}
	SavePlacement(d, p)
	if len(d) != 1 || d[PlacementKey] == "" {
		t.Fatalf("saved %v, want one value under %q", d, PlacementKey)
	}
	if !strings.HasPrefix(PlacementKey, brand.LaunchAgent()+".") {
		t.Fatalf("key %q is outside the brand's domain %q", PlacementKey, brand.LaunchAgent())
	}
	got, ok := LoadPlacement(d)
	if !ok || got != p {
		t.Fatalf("loaded %+v %v, want %+v", got, ok, p)
	}
}

// A placement saved without a size loads with the size the float starts at.
func TestAPlacementWithoutASizeLoadsWithTheStartSize(t *testing.T) {
	p, ok := LoadPlacement(memDefaults{PlacementKey: `{"corner":"tl","x":10,"y":50,"w":0,"h":0}`})
	if want := (FloatPlacement{Corner: CornerTopLeft, X: 10, Y: 50, W: floatStartW, H: floatStartH}); !ok || p != want {
		t.Fatalf("loaded %+v %v, want %+v", p, ok, want)
	}
}

func TestAnUnreadablePlacementLoadsAsNone(t *testing.T) {
	for _, v := range []string{"{", `{"corner":"middle"}`, `{"corner":"tl","edge":"top"}`} {
		if p, ok := LoadPlacement(memDefaults{PlacementKey: v}); ok {
			t.Errorf("%q loaded as %+v", v, p)
		}
	}
}

func TestParseFloatMessage(t *testing.T) {
	cases := []struct {
		raw  string
		want FloatMessage
		ok   bool
	}{
		{`{"type":"float:size","w":460,"h":600,"mode":"panel"}`, FloatMessage{Kind: FloatSize, W: 460, H: 600, Mode: ModePanel}, true},
		{`{"type":"float:size","w":128,"h":48,"mode":"pill"}`, FloatMessage{Kind: FloatSize, W: 128, H: 48, Mode: ModePill}, true},
		{`{"type":"float:open","route":"task=t-1"}`, FloatMessage{Kind: FloatOpen, Route: "task=t-1"}, true},
		{`{"type":"float:hide","mode":"quiet"}`, FloatMessage{Kind: FloatHide, Mode: "quiet"}, true},
		{`{"type":"float:size","w":0,"h":32,"mode":"pill"}`, FloatMessage{}, false},
		{`{"type":"float:move","x":1}`, FloatMessage{}, false},
		{`float:size {"w":460,"h":600}`, FloatMessage{}, false},
		{`{"w":460,"h":600}`, FloatMessage{}, false},
		{`hello`, FloatMessage{}, false},
	}
	for _, c := range cases {
		got, ok := ParseFloatMessage(c.raw)
		if ok != c.ok || got != c.want {
			t.Errorf("%q: %+v %v, want %+v %v", c.raw, got, ok, c.want, c.ok)
		}
	}
}

func TestTheFloatTakesTheKeyboardOnlyInPanelMode(t *testing.T) {
	for mode, want := range map[string]bool{ModePill: false, ModePeek: false, ModePanel: true, "": false, "other": false} {
		if got := FloatKeyable(mode); got != want {
			t.Errorf("mode %q keyable %v, want %v", mode, got, want)
		}
	}
}

// floatRig is a running controller whose host listened and reported screens.
func floatRig(t *testing.T, d Defaults, screens ...Screen) *rig {
	t.Helper()
	r := &rig{host: &fakeHost{}, events: &fakeEvents{streams: []chan api.Change{make(chan api.Change)}, stop: make(chan struct{})}, logs: &logs{}}
	r.ctl = New(Config{Host: r.host, Daemon: r.events, CockpitURL: cockpitURL, Log: r.logs.logf, Defaults: d})
	r.run(t)
	synctest.Wait()
	r.host.handlers().OnScreens(screens)
	return r
}

// size is a size message of the pill.
func size(w, h float64) FloatMessage {
	return FloatMessage{Kind: FloatSize, W: w, H: h, Mode: ModePill}
}

// shownRig is a floatRig whose float shows and whose page has loaded, with
// the host calls so far taken.
func shownRig(t *testing.T, d Defaults, screens ...Screen) *rig {
	t.Helper()
	r := floatRig(t, d, screens...)
	if err := r.ctl.ShowFloat(); err != nil {
		t.Fatal(err)
	}
	r.host.handlers().OnFloat(size(112, 32))
	r.host.taken()
	return r
}

// Growing from the bottom right, the frame keeps its right and bottom edges
// where they were: the pill stays under the mouse while the panel opens.
// The page learns where it sits once it has loaded.
func TestFloatSizeKeepsAnchorCorner(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		d := memDefaults{}
		SavePlacement(d, FloatPlacement{Corner: CornerBottomRight, X: 1400, Y: 900, W: 112, H: 32})
		r := floatRig(t, d, laptop)
		if err := r.ctl.ShowFloat(); err != nil {
			t.Fatal(err)
		}
		want(t, r.host.taken(), "float-frame {1288 868 112 32}", "float-load "+base+"#"+FloatRoute)
		r.host.handlers().OnFloat(FloatMessage{Kind: FloatSize, W: 460, H: 600, Mode: ModePanel})
		r.host.handlers().OnFloat(size(112, 32))
		want(t, r.host.taken(), "float-frame {940 300 460 600}", `float-place "" true 1512x911`, "float-frame {1288 868 112 32}")
	})
}

// A size larger than the screen's visible frame is capped to it.
func TestFloatSizeIsCappedToTheScreen(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		d := memDefaults{}
		SavePlacement(d, FloatPlacement{Corner: CornerBottomRight, X: 1400, Y: 900, W: 112, H: 32})
		r := shownRig(t, d, laptop)
		r.host.handlers().OnFloat(FloatMessage{Kind: FloatSize, W: 2000, H: 1200, Mode: ModePanel})
		want(t, r.host.taken(), "float-frame {0 33 1512 911}")
	})
}

// A size the page sends while the float is hidden moves nothing.
func TestFloatSizeWhileHiddenMovesNothing(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := floatRig(t, nil, laptop)
		r.host.handlers().OnFloat(size(460, 600))
		want(t, r.host.taken())
	})
}

// Without a saved placement the float docks on the main screen's right edge.
func TestTheFloatStartsDockedRight(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := floatRig(t, memDefaults{}, laptop, wide)
		if err := r.ctl.ShowFloat(); err != nil {
			t.Fatal(err)
		}
		got := r.host.taken()
		if len(got) != 2 || !strings.HasPrefix(got[0], "float-frame {1400 ") {
			t.Fatalf("host calls %q, want a frame flush with the laptop's right edge", got)
		}
		r.host.handlers().OnFloat(size(34, 84))
		want(t, r.host.taken(), "float-frame {1478 260.75 34 84}", `float-place "right" false 1512x911`)
	})
}

// A peek beside a tab docked on the right grows the frame leftward, keeping
// the tab flush with the screen's edge, and its end shrinks it back.
func TestAPeekBesideARightTabGrowsTheFrameLeftward(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		d := memDefaults{}
		SavePlacement(d, FloatPlacement{Edge: EdgeRight, Corner: CornerTopRight, X: 1512, Y: 200, W: 34, H: 84})
		r := shownRig(t, d, laptop)
		r.host.handlers().OnFloat(FloatMessage{Kind: FloatSize, W: 400, H: 84, Mode: ModePeek})
		r.host.handlers().OnFloat(size(34, 84))
		want(t, r.host.taken(), "float-frame {1112 200 400 84}", "float-frame {1478 200 34 84}")
	})
}

// Unplugging the screen the pill sits on brings it back on the main screen;
// plugging it back returns the pill where the person left it. Each time the
// page learns where it sits and the size of the screen that now holds it.
func TestScreensChangingMoveTheFloatBack(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		d := memDefaults{}
		SavePlacement(d, FloatPlacement{Corner: CornerBottomRight, X: 3900, Y: 1300, W: 112, H: 32})
		r := shownRig(t, d, laptop, wide)
		r.host.handlers().OnScreens([]Screen{laptop})
		r.host.handlers().OnScreens([]Screen{laptop, wide})
		want(t, r.host.taken(),
			"float-frame {1400 912 112 32}", `float-place "" true 1512x911`,
			"float-frame {3788 1268 112 32}", `float-place "" true 2560x1390`)
	})
}

// A drop is saved and snapped to the edge it was dropped near at once; the
// moves before it save nothing.
func TestADropIsSavedAndSnapped(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		d := memDefaults{}
		r := shownRig(t, d, laptop)
		saved := d[PlacementKey]
		r.host.handlers().OnFloatMoving()
		if d[PlacementKey] != saved {
			t.Fatal("a move saved the placement before the drop")
		}
		r.host.handlers().OnFloatDropped(Rect{X: 10, Y: 500, W: 112, H: 32})
		got, ok := LoadPlacement(d)
		if want := (FloatPlacement{Edge: EdgeLeft, Corner: CornerBottomLeft, X: 0, Y: 532, W: 112, H: 32}); !ok || got != want {
			t.Fatalf("saved %+v %v, want %+v", got, ok, want)
		}
		want(t, r.host.taken(), "float-frame {0 500 112 32}", `float-place "left" true 1512x911`)
	})
}

// A size that arrives during a drag waits for the drop, so the frame never
// jumps under the dragging mouse; the drop applies it from the new anchor.
func TestASizeDuringADragWaitsForTheDrop(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		d := memDefaults{}
		r := shownRig(t, d, laptop)
		r.host.handlers().OnFloatMoving()
		r.host.handlers().OnFloat(size(34, 84))
		want(t, r.host.taken())
		r.host.handlers().OnFloatDropped(Rect{X: 10, Y: 500, W: 112, H: 32})
		want(t, r.host.taken(), "float-frame {0 448 34 84}", `float-place "left" true 1512x911`)
		if got, _ := LoadPlacement(d); got.W != 34 || got.H != 84 {
			t.Fatalf("saved %+v, want the size sent during the drag", got)
		}
	})
}

func TestFloatOpenShowsTheRouteInTheMainWindow(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := floatRig(t, nil, laptop)
		r.host.handlers().OnFloat(FloatMessage{Kind: FloatOpen, Route: "task=t-1"})
		want(t, r.host.taken(), "main "+base+"#task=t-1", "hide-panel")
	})
}

func TestFloatHideReachesItsHook(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := floatRig(t, nil, laptop)
		r.host.handlers().OnFloat(FloatMessage{Kind: FloatHide, Mode: "quiet"})
		var got []string
		r.ctl.OnFloatHide(func(mode string) { got = append(got, mode) })
		r.host.handlers().OnFloat(FloatMessage{Kind: FloatHide, Mode: "quiet"})
		if len(got) != 1 || got[0] != "quiet" {
			t.Fatalf("hook got %q", got)
		}
		want(t, r.host.taken())
	})
}

func TestHideFloat(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := shownRig(t, nil, laptop)
		r.ctl.HideFloat()
		r.host.handlers().OnFloat(size(460, 600))
		want(t, r.host.taken(), "hide-float")
	})
}

func TestTheHostListensWithTheFullScreenSetting(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		for _, over := range []bool{false, true} {
			r := &rig{host: &fakeHost{}, events: &fakeEvents{streams: []chan api.Change{make(chan api.Change)}, stop: make(chan struct{})}, logs: &logs{}}
			r.ctl = New(Config{Host: r.host, Daemon: r.events, CockpitURL: cockpitURL, FloatOverFullScreen: over})
			r.run(t)
			synctest.Wait()
			if got := r.host.handlers().FloatOverFullScreen; got != over {
				t.Fatalf("listened with FloatOverFullScreen %v, want %v", got, over)
			}
		}
	})
}

func TestTheBridgeIsNamedAfterTheBrandsCommand(t *testing.T) {
	if FloatBridge != brand.Command+"Float" {
		t.Fatalf("bridge %q", FloatBridge)
	}
}

func TestTheFloatHotkeyReachesItsHook(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := &rig{host: &fakeHost{}, events: &fakeEvents{streams: []chan api.Change{make(chan api.Change)}, stop: make(chan struct{})}, logs: &logs{}}
		r.ctl = New(Config{Host: r.host, Daemon: r.events, CockpitURL: cockpitURL, FloatHotkey: "cmd+option+."})
		r.run(t)
		synctest.Wait()
		hs := r.host.handlers()
		if hs.FloatHotkey != "cmd+option+." || hs.OnFloatHotkey == nil {
			t.Fatalf("listened with float hotkey %q, handler set %v", hs.FloatHotkey, hs.OnFloatHotkey != nil)
		}
		hs.OnFloatHotkey()
		pressed := 0
		r.ctl.OnFloatHotkey(func() { pressed++ })
		hs.OnFloatHotkey()
		if pressed != 1 {
			t.Fatalf("hook ran %d times, want 1", pressed)
		}
		want(t, r.host.taken())
	})
}

// downRig is a controller whose daemon does not answer yet: its cockpit URL
// fails while down holds, and its events serve streams in turn.
func downRig(down *atomic.Bool, streams ...chan api.Change) *rig {
	r := &rig{host: &fakeHost{}, events: &fakeEvents{streams: streams, stop: make(chan struct{})}, logs: &logs{}}
	url := func(route string) (string, error) {
		if down.Load() {
			return "", errors.New("no http.json: the daemon is not running")
		}
		return cockpitURL(route)
	}
	r.ctl = New(Config{Host: r.host, Daemon: r.events, CockpitURL: url, Log: r.logs.logf})
	return r
}

// A float shown before the daemon could serve its page shows once the
// daemon's events answer, though the first subscription already does.
func TestAFloatTheDaemonCouldNotServeShowsOnceItsEventsAnswer(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var down atomic.Bool
		down.Store(true)
		r := downRig(&down, make(chan api.Change))
		if err := r.ctl.ShowFloat(); err == nil {
			t.Fatal("ShowFloat with no cockpit URL answered no error")
		}
		want(t, r.host.taken())
		down.Store(false)
		r.run(t)
		synctest.Wait()
		want(t, r.host.taken(), "float-load "+base+"#"+FloatRoute)
	})
}

// A float whose page the daemon refused, as it went down, loads its page
// anew when the daemon's events come back, at the same URL.
func TestARefusedFloatPageLoadsAnewWhenTheDaemonComesBack(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var down atomic.Bool
		r := downRig(&down, nil, make(chan api.Change))
		if err := r.ctl.ShowFloat(); err != nil {
			t.Fatal(err)
		}
		want(t, r.host.taken(), "float-load "+base+"#"+FloatRoute)
		r.run(t)
		synctest.Wait()
		want(t, r.host.taken())
		time.Sleep(reconnectAfter)
		synctest.Wait()
		want(t, r.host.taken(), "float-load "+base+"#"+FloatRoute)
	})
}

// A hidden float stays hidden when the daemon comes back.
func TestAHiddenFloatStaysHiddenWhenTheDaemonComesBack(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var down atomic.Bool
		down.Store(true)
		r := downRig(&down, nil, make(chan api.Change))
		_ = r.ctl.ShowFloat()
		r.ctl.HideFloat()
		r.host.taken()
		down.Store(false)
		r.run(t)
		time.Sleep(reconnectAfter)
		synctest.Wait()
		want(t, r.host.taken())
	})
}

// A float whose page loaded and is shown again at the same URL keeps its
// page, which learns where it sits at once.
func TestAFloatShownAgainKeepsItsLoadedPageAndIsPlacedAtOnce(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := shownRig(t, memDefaults{}, laptop)
		r.ctl.HideFloat()
		if err := r.ctl.ShowFloat(); err != nil {
			t.Fatal(err)
		}
		got := r.host.taken()
		if len(got) != 4 || got[0] != "hide-float" || !strings.HasPrefix(got[2], "float-place ") || got[3] != "float "+base+"#"+FloatRoute {
			t.Fatalf("host calls %q, want the frame, the place and a show without a load", got)
		}
	})
}

// A float whose page loaded keeps it when the daemon restarts at the same
// URL; the page follows the daemon's events on its own.
func TestAFloatPageThatLoadedIsKeptAcrossADaemonRestart(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		first := make(chan api.Change)
		r := &rig{host: &fakeHost{}, events: &fakeEvents{streams: []chan api.Change{first, make(chan api.Change)}, stop: make(chan struct{})}, logs: &logs{}}
		r.ctl = New(Config{Host: r.host, Daemon: r.events, CockpitURL: cockpitURL, Log: r.logs.logf})
		r.run(t)
		synctest.Wait()
		r.host.handlers().OnScreens([]Screen{laptop})
		if err := r.ctl.ShowFloat(); err != nil {
			t.Fatal(err)
		}
		r.host.handlers().OnFloat(size(112, 32))
		r.host.taken()
		close(first)
		time.Sleep(reconnectAfter)
		synctest.Wait()
		for _, c := range r.host.taken() {
			if strings.HasPrefix(c, "float-load ") {
				t.Fatalf("the restart reloaded the float's page: %q", c)
			}
		}
	})
}

// The float reports ready only once its page has reported in since it was
// last loaded, and says so to its hook once.
func TestTheFloatIsReadyOnceItsPageReportsIn(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := floatRig(t, nil, laptop)
		readies := 0
		r.ctl.OnFloatReady(func() { readies++ })
		if err := r.ctl.ShowFloat(); err != nil {
			t.Fatal(err)
		}
		if r.ctl.FloatReady() {
			t.Fatal("ready before its page reported in")
		}
		r.host.handlers().OnFloat(size(112, 32))
		r.host.handlers().OnFloat(size(460, 600))
		if !r.ctl.FloatReady() || readies != 1 {
			t.Fatalf("ready %v, hook ran %d times; want ready, once", r.ctl.FloatReady(), readies)
		}
		r.ctl.HideFloat()
		if r.ctl.FloatReady() {
			t.Fatal("a hidden float reports ready")
		}
	})
}

// A float whose page has not reported in loads it anew on the first
// subscription too, as when a stale http.json named a daemon that was still
// starting.
func TestAFloatPageNotYetReadyLoadsAnewOnTheFirstSubscription(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var down atomic.Bool
		r := downRig(&down, make(chan api.Change))
		if err := r.ctl.ShowFloat(); err != nil {
			t.Fatal(err)
		}
		r.host.taken()
		r.run(t)
		synctest.Wait()
		want(t, r.host.taken(), "float-load "+base+"#"+FloatRoute)
	})
}
