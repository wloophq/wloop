package window

import (
	"context"
	"errors"
	"strings"
	"sync"
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
		{`{"type":"float:size","w":476,"h":600,"mode":"panel"}`, FloatMessage{Kind: FloatSize, W: 476, H: 600, Mode: ModePanel}, true},
		{`{"type":"float:size","w":128,"h":48}`, FloatMessage{Kind: FloatSize, W: 128, H: 48}, true},
		{`{"type":"float:open","route":"task=t-1"}`, FloatMessage{Kind: FloatOpen, Route: "task=t-1"}, true},
		{`{"type":"float:hide","mode":"quiet"}`, FloatMessage{Kind: FloatHide, Mode: "quiet"}, true},
		{`{"type":"float:panel","toggle":true}`, FloatMessage{Kind: FloatOpenPanel, Toggle: true}, true},
		{`{"type":"float:peek","item":"gate:7"}`, FloatMessage{Kind: FloatPeek, Item: "gate:7"}, true},
		{`{"type":"float:peek"}`, FloatMessage{}, false},
		{`{"type":"float:close"}`, FloatMessage{Kind: FloatClose}, true},
		{`{"type":"float:state","read":"failed"}`, FloatMessage{Kind: FloatState, Read: ReadFailed}, true},
		{`{"type":"float:state","read":"ok"}`, FloatMessage{Kind: FloatState, Read: ReadOK}, true},
		{`{"type":"float:state","read":"later"}`, FloatMessage{}, false},
		{`{"type":"float:size","w":0,"h":32}`, FloatMessage{}, false},
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

func TestThePanelTakesTheKeyboardOnlyInPanelMode(t *testing.T) {
	for mode, want := range map[string]bool{ModePeek: false, ModePanel: true, "": false, "other": false} {
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

// size is a size message of the tab.
func size(w, h float64) FloatMessage {
	return FloatMessage{Window: FloatTab, Kind: FloatSize, W: w, H: h}
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

// A tab anchored at the bottom right keeps its right and bottom edges where
// they were as its counts widen it. The page learns where it sits once it
// has loaded.
func TestFloatSizeKeepsAnchorCorner(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		d := memDefaults{}
		SavePlacement(d, FloatPlacement{Corner: CornerBottomRight, X: 1400, Y: 900, W: 112, H: 32})
		r := floatRig(t, d, laptop)
		if err := r.ctl.ShowFloat(); err != nil {
			t.Fatal(err)
		}
		want(t, r.host.taken(), "float-frame {1288 868 112 32}", "float-load "+base+"#"+FloatTabRoute)
		r.host.handlers().OnFloat(size(140, 32))
		r.host.handlers().OnFloat(size(140, 32))
		want(t, r.host.taken(), "float-frame {1260 868 140 32}", `float-place ""`)
	})
}

// A size larger than the screen's visible frame is capped to it.
func TestFloatSizeIsCappedToTheScreen(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		d := memDefaults{}
		SavePlacement(d, FloatPlacement{Corner: CornerBottomRight, X: 1400, Y: 900, W: 112, H: 32})
		r := shownRig(t, d, laptop)
		r.host.handlers().OnFloat(size(2000, 1200))
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
		want(t, r.host.taken(), "float-frame {1478 260.75 34 84}", `float-place "right"`)
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
			"float-frame {1400 912 112 32}", `float-place ""`,
			"float-frame {3788 1268 112 32}", `float-place ""`)
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
		want(t, r.host.taken(), "float-frame {0 500 112 32}", `float-place "left"`)
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
		want(t, r.host.taken(), "float-frame {0 448 34 84}", `float-place "left"`)
		if got, _ := LoadPlacement(d); got.W != 34 || got.H != 84 {
			t.Fatalf("saved %+v, want the size sent during the drag", got)
		}
	})
}

func TestFloatOpenShowsTheRouteInTheMainWindow(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := floatRig(t, nil, laptop)
		r.host.handlers().OnFloat(FloatMessage{Window: FloatPanel, Kind: FloatOpen, Route: "task=t-1"})
		want(t, r.host.taken(), "main "+base+"#task=t-1", "hide-panel")
	})
}

func TestFloatHideReachesItsHook(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := floatRig(t, nil, laptop)
		r.host.handlers().OnFloat(FloatMessage{Window: FloatTab, Kind: FloatHide, Mode: "quiet"})
		var got []string
		r.ctl.OnFloatHide(func(mode string) { got = append(got, mode) })
		r.host.handlers().OnFloat(FloatMessage{Window: FloatTab, Kind: FloatHide, Mode: "quiet"})
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
		want(t, r.host.taken(), "float-load "+base+"#"+FloatTabRoute)
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
		want(t, r.host.taken(), "float-load "+base+"#"+FloatTabRoute)
		r.run(t)
		synctest.Wait()
		want(t, r.host.taken())
		time.Sleep(reconnectAfter)
		synctest.Wait()
		want(t, r.host.taken(), "float-load "+base+"#"+FloatTabRoute)
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
		if len(got) != 4 || got[0] != "hide-float" || !strings.HasPrefix(got[2], "float-place ") || got[3] != "float "+base+"#"+FloatTabRoute {
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

// readState is the tab's report of how its read of the queue went.
func readState(read string) FloatMessage {
	return FloatMessage{Window: FloatTab, Kind: FloatState, Read: read}
}

// The float reports ready only once its page has said how its read of the
// queue went since it was last loaded, and says so to its hook once.
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
		r.host.handlers().OnFloat(FloatMessage{Window: FloatPanel, Kind: FloatClose})
		r.host.handlers().OnFloat(size(112, 32))
		if r.ctl.FloatReady() || readies != 0 {
			t.Fatal("a page that has not said how its read went made the float ready")
		}
		r.host.handlers().OnFloat(readState(ReadOK))
		r.host.handlers().OnFloat(readState(ReadOK))
		if !r.ctl.FloatReady() || readies != 1 {
			t.Fatalf("ready %v, hook ran %d times; want ready, once", r.ctl.FloatReady(), readies)
		}
		r.ctl.HideFloat()
		if r.ctl.FloatReady() {
			t.Fatal("a hidden float reports ready")
		}
	})
}

// A tab whose page says its read failed or the daemon is offline still
// carries the queue for FloatDownAfter, so one failed read posts no banner;
// past it, it is not ready and its hook hears it once. A tab whose web content
// process ended carries nothing at once; its web view reloads the page.
func TestATabThatCannotShowTheQueueIsNotReady(t *testing.T) {
	for _, read := range []string{ReadFailed, ReadOffline} {
		t.Run(read, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				r := shownRig(t, nil, laptop)
				var changes atomic.Int32
				r.ctl.OnFloatReady(func() { changes.Add(1) })
				r.host.handlers().OnFloat(readState(ReadOK))
				changes.Store(0)
				r.host.handlers().OnFloat(readState(read))
				time.Sleep(FloatDownAfter / 2)
				r.host.handlers().OnFloat(readState(ReadOK))
				r.host.handlers().OnFloat(readState(read))
				time.Sleep(FloatDownAfter - time.Nanosecond)
				synctest.Wait()
				if !r.ctl.FloatReady() || changes.Load() != 0 {
					t.Fatalf("ready %v, hook ran %d times; want a tab down for less than FloatDownAfter still ready", r.ctl.FloatReady(), changes.Load())
				}
				r.host.handlers().OnFloat(readState(read))
				time.Sleep(time.Nanosecond)
				synctest.Wait()
				if r.ctl.FloatReady() || changes.Load() != 1 {
					t.Fatalf("ready %v, hook ran %d times; want not ready, once, after FloatDownAfter", r.ctl.FloatReady(), changes.Load())
				}
				r.host.handlers().OnFloat(readState(ReadOK))
				if !r.ctl.FloatReady() || changes.Load() != 2 {
					t.Fatalf("ready %v, hook ran %d times; want ready again", r.ctl.FloatReady(), changes.Load())
				}
				r.host.taken()
				r.host.handlers().OnFloatGone(FloatTab)
				if r.ctl.FloatReady() || changes.Load() != 3 {
					t.Fatalf("ready %v, hook ran %d times; want not ready once its process ended", r.ctl.FloatReady(), changes.Load())
				}
				if calls := r.host.taken(); len(calls) != 0 {
					t.Fatalf("host calls %q, want none: the web view reloads its page", calls)
				}
				r.host.handlers().OnFloat(readState(ReadOK))
				if !r.ctl.FloatReady() || changes.Load() != 4 {
					t.Fatalf("ready %v, hook ran %d times; want ready as the reloaded page reports in", r.ctl.FloatReady(), changes.Load())
				}
			})
		})
	}
}

// A tab whose page stops reporting its read, as one whose script hangs, is
// not ready FloatSilentAfter after its last report, and its hook hears it.
func TestATabThatStopsReportingIsNotReady(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := shownRig(t, nil, laptop)
		var changes atomic.Int32
		r.ctl.OnFloatReady(func() { changes.Add(1) })
		r.host.handlers().OnFloat(readState(ReadOK))
		for range 3 {
			time.Sleep(FloatSilentAfter - time.Nanosecond)
			r.host.handlers().OnFloat(readState(ReadOK))
		}
		synctest.Wait()
		if !r.ctl.FloatReady() || changes.Load() != 1 {
			t.Fatalf("ready %v, hook ran %d times; want a tab that keeps reporting ready", r.ctl.FloatReady(), changes.Load())
		}
		time.Sleep(FloatSilentAfter)
		synctest.Wait()
		if r.ctl.FloatReady() || changes.Load() != 2 {
			t.Fatalf("ready %v, hook ran %d times; want not ready FloatSilentAfter after its last report", r.ctl.FloatReady(), changes.Load())
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
		want(t, r.host.taken(), "float-load "+base+"#"+FloatTabRoute)
	})
}

// The panel goes beside the tab, away from a docked edge, or below or above
// a free pill, whichever has more room; it lines up with the tab's side
// nearer the screen's middle and stays inside the visible frame on its own.
func TestThePanelGoesBesideTheTab(t *testing.T) {
	for _, c := range []struct {
		name  string
		tab   Rect
		edge  string
		w, h  float64
		on    []Screen
		side  string
		frame Rect
	}{
		{"docked right, high", Rect{X: 1478, Y: 260.75, W: 34, H: 84}, EdgeRight, 476, 600, []Screen{laptop}, SideLeft, Rect{X: 1002, Y: 260.75, W: 476, H: 600}},
		{"docked right, low", Rect{X: 1478, Y: 800, W: 34, H: 84}, EdgeRight, 476, 600, []Screen{laptop}, SideLeft, Rect{X: 1002, Y: 284, W: 476, H: 600}},
		{"docked left, taller than the room below it", Rect{X: 0, Y: 400, W: 34, H: 84}, EdgeLeft, 476, 900, []Screen{laptop}, SideRight, Rect{X: 34, Y: 44, W: 476, H: 900}},
		{"docked left, taller than the screen", Rect{X: 0, Y: 400, W: 34, H: 84}, EdgeLeft, 476, 2000, []Screen{laptop}, SideRight, Rect{X: 34, Y: 33, W: 476, H: 911}},
		{"free, top left", Rect{X: 100, Y: 100, W: 112, H: 32}, "", 476, 600, []Screen{laptop}, SideBelow, Rect{X: 100, Y: 132, W: 476, H: 600}},
		{"free, bottom right", Rect{X: 1300, Y: 880, W: 112, H: 32}, "", 476, 600, []Screen{laptop}, SideAbove, Rect{X: 936, Y: 280, W: 476, H: 600}},
		{"free, taller than the room below", Rect{X: 1300, Y: 300, W: 112, H: 32}, "", 476, 800, []Screen{laptop}, SideBelow, Rect{X: 936, Y: 332, W: 476, H: 612}},
		{"free, wider than the screen", Rect{X: 100, Y: 100, W: 112, H: 32}, "", 2000, 300, []Screen{laptop}, SideBelow, Rect{X: 0, Y: 132, W: 1512, H: 300}},
		{"docked right on the second screen", Rect{X: 4038, Y: 500, W: 34, H: 84}, EdgeRight, 476, 600, []Screen{laptop, wide}, SideLeft, Rect{X: 3562, Y: 500, W: 476, H: 600}},
	} {
		t.Run(c.name, func(t *testing.T) {
			side, _ := PanelRoom(c.tab, c.edge, c.on)
			if side != c.side {
				t.Fatalf("side %q, want %q", side, c.side)
			}
			wantRect(t, PanelFrame(c.tab, c.edge, c.w, c.h, c.on), c.frame)
		})
	}
}

// The room the panel's page is told is where PanelFrame may put it: a panel
// of that size fills it exactly.
func TestThePanelsRoomIsWhereItGoes(t *testing.T) {
	tab := Rect{X: 1478, Y: 260.75, W: 34, H: 84}
	side, room := PanelRoom(tab, EdgeRight, []Screen{laptop})
	if want := (Rect{X: 0, Y: 33, W: 1478, H: 911}); side != SideLeft || room != want {
		t.Fatalf("room %q %+v, want %q %+v", side, room, SideLeft, want)
	}
	wantRect(t, PanelFrame(tab, EdgeRight, room.W, room.H, []Screen{laptop}), room)
}

// Wherever the tab sits and however large the panel, the panel never covers
// the tab and stays inside the visible frame of the tab's screen.
func TestThePanelNeverCoversTheTabNorLeavesTheScreen(t *testing.T) {
	screens := []Screen{laptop, wide}
	for _, s := range screens {
		v := s.Visible
		for _, edge := range []string{"", EdgeLeft, EdgeRight} {
			for fy := 0.0; fy <= 1; fy += 0.125 {
				for fx := 0.0; fx <= 1; fx += 0.125 {
					for _, size := range [][2]float64{{340, 60}, {476, 600}, {476, 5000}, {5000, 300}} {
						tab := Rect{W: 112, H: 32}
						if edge != "" {
							tab.W, tab.H = 34, 84
						}
						tab.X = v.X + fx*(v.W-tab.W)
						tab.Y = v.Y + fy*(v.H-tab.H)
						switch edge {
						case EdgeLeft:
							tab.X = v.X
						case EdgeRight:
							tab.X = v.right() - tab.W
						}
						p := PanelFrame(tab, edge, size[0], size[1], screens)
						if a := p.overlap(tab); a > 0 {
							t.Fatalf("edge %q tab %+v size %v: panel %+v covers the tab by %v", edge, tab, size, p, a)
						}
						if p.X < v.X || p.Y < v.Y || p.right() > v.right() || p.bottom() > v.bottom() {
							t.Fatalf("edge %q tab %+v size %v: panel %+v leaves %+v", edge, tab, size, p, v)
						}
					}
				}
			}
		}
	}
}

// panelMsg is a message of the panel's page.
func panelMsg(m FloatMessage) FloatMessage {
	m.Window = FloatPanel
	return m
}

// dockedRig is a shownRig whose tab is docked on the laptop's right edge,
// a quarter down.
func dockedRig(t *testing.T) *rig {
	t.Helper()
	d := memDefaults{}
	SavePlacement(d, FloatPlacement{Edge: EdgeRight, Corner: CornerTopRight, X: 1512, Y: 260.75, W: 34, H: 84})
	r := shownRig(t, d, laptop)
	r.host.handlers().OnFloat(size(34, 84))
	r.host.taken()
	return r
}

const panelURL = base + "#" + FloatPanelRoute

// The panel's window loads its page on the first open only, is told what to
// show once that page reports in, and shows beside the tab at the size it
// then reports; the tab's frame never changes.
func TestThePanelLoadsOnItsFirstOpenAndShowsBesideTheTab(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := dockedRig(t)
		r.host.handlers().OnFloat(FloatMessage{Window: FloatTab, Kind: FloatOpenPanel})
		want(t, r.host.taken(), "fpanel-load "+panelURL)
		r.host.handlers().OnFloat(panelMsg(FloatMessage{Kind: FloatClose}))
		want(t, r.host.taken(), `fpanel-tell "panel" "" "left" 1478x911`)
		r.host.handlers().OnFloat(panelMsg(FloatMessage{Kind: FloatSize, W: 476, H: 600, Mode: ModePanel}))
		r.host.handlers().OnFloat(panelMsg(FloatMessage{Kind: FloatSize, W: 476, H: 640, Mode: ModePanel}))
		want(t, r.host.taken(), "fpanel-show {1002 260.75 476 600} panel", "fpanel-show {1002 260.75 476 640} panel")

		r.host.handlers().OnFloat(panelMsg(FloatMessage{Kind: FloatClose}))
		want(t, r.host.taken(), "fpanel-hide", `fpanel-tell "" "" "" 0x0`)
		r.host.handlers().OnFloat(FloatMessage{Window: FloatTab, Kind: FloatOpenPanel})
		want(t, r.host.taken(), `fpanel-tell "panel" "" "left" 1478x911`)
	})
}

// A peek asks the panel's page to show the item that arrived; one that
// arrives while the full panel shows is dropped.
func TestAPeekShowsInThePanelWindowUnlessThePanelIsOpen(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := dockedRig(t)
		r.host.handlers().OnFloat(FloatMessage{Window: FloatTab, Kind: FloatPeek, Item: "gate:7"})
		r.host.handlers().OnFloat(panelMsg(FloatMessage{Kind: FloatClose}))
		r.host.handlers().OnFloat(panelMsg(FloatMessage{Kind: FloatSize, W: 356, H: 60, Mode: ModePeek}))
		want(t, r.host.taken(), "fpanel-load "+panelURL, `fpanel-tell "peek" "gate:7" "left" 1478x911`, "fpanel-show {1122 260.75 356 60} peek")

		r.host.handlers().OnFloat(FloatMessage{Window: FloatTab, Kind: FloatOpenPanel})
		r.host.handlers().OnFloat(panelMsg(FloatMessage{Kind: FloatSize, W: 476, H: 600, Mode: ModePanel}))
		r.host.taken()
		r.host.handlers().OnFloat(FloatMessage{Window: FloatTab, Kind: FloatPeek, Item: "gate:8"})
		want(t, r.host.taken())
	})
}

// A click on the tab opens the panel, and another closes it, telling its page.
func TestAToggleClosesAnOpenPanel(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := dockedRig(t)
		r.host.handlers().OnFloat(FloatMessage{Window: FloatTab, Kind: FloatOpenPanel, Toggle: true})
		r.host.handlers().OnFloat(panelMsg(FloatMessage{Kind: FloatClose}))
		r.host.handlers().OnFloat(panelMsg(FloatMessage{Kind: FloatSize, W: 476, H: 600, Mode: ModePanel}))
		r.host.taken()
		r.host.handlers().OnFloat(FloatMessage{Window: FloatTab, Kind: FloatOpenPanel, Toggle: true})
		want(t, r.host.taken(), "fpanel-hide", `fpanel-tell "" "" "" 0x0`)
		r.host.handlers().OnFloat(panelMsg(FloatMessage{Kind: FloatClose}))
		want(t, r.host.taken())
	})
}

// Dragging the tab closes the panel; hiding the float hides both windows.
func TestADragOrAHideClosesThePanel(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := dockedRig(t)
		open := func() {
			r.host.handlers().OnFloat(FloatMessage{Window: FloatTab, Kind: FloatOpenPanel})
			r.host.handlers().OnFloat(panelMsg(FloatMessage{Kind: FloatSize, W: 476, H: 600, Mode: ModePanel}))
			r.host.taken()
		}
		r.host.handlers().OnFloat(FloatMessage{Window: FloatTab, Kind: FloatOpenPanel})
		r.host.handlers().OnFloat(panelMsg(FloatMessage{Kind: FloatClose}))
		open()
		r.host.handlers().OnFloatMoving()
		want(t, r.host.taken(), "fpanel-hide", `fpanel-tell "" "" "" 0x0`)
		r.host.handlers().OnFloatDropped(Rect{X: 1478, Y: 260.75, W: 34, H: 84})
		r.host.taken()
		open()
		r.ctl.HideFloat()
		want(t, r.host.taken(), "fpanel-hide", `fpanel-tell "" "" "" 0x0`, "hide-float")
		r.host.handlers().OnFloat(FloatMessage{Window: FloatTab, Kind: FloatOpenPanel})
		want(t, r.host.taken())
	})
}

// A panel the daemon moved to another address loads anew on the next open;
// the same address keeps its page.
func TestThePanelLoadsAnewAtANewAddress(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		port := "7000"
		r := &rig{host: &fakeHost{}, events: &fakeEvents{streams: []chan api.Change{make(chan api.Change)}, stop: make(chan struct{})}, logs: &logs{}}
		var mu sync.Mutex
		url := func(route string) (string, error) {
			mu.Lock()
			defer mu.Unlock()
			return "http://127.0.0.1:" + port + "/#" + route, nil
		}
		r.ctl = New(Config{Host: r.host, Daemon: r.events, CockpitURL: url})
		r.run(t)
		synctest.Wait()
		r.host.handlers().OnScreens([]Screen{laptop})
		if err := r.ctl.ShowFloat(); err != nil {
			t.Fatal(err)
		}
		r.host.handlers().OnFloat(size(34, 84))
		r.host.handlers().OnFloat(FloatMessage{Window: FloatTab, Kind: FloatOpenPanel})
		r.host.handlers().OnFloat(panelMsg(FloatMessage{Kind: FloatClose}))
		r.host.taken()
		mu.Lock()
		port = "7001"
		mu.Unlock()
		r.host.handlers().OnFloat(FloatMessage{Window: FloatTab, Kind: FloatOpenPanel})
		if got := r.host.taken(); len(got) != 1 || got[0] != "fpanel-load http://127.0.0.1:7001/#"+FloatPanelRoute {
			t.Fatalf("host calls %q, want the panel loaded at the new address", got)
		}
	})
}

// A size the panel's page reports while nothing asked it to show, as a late
// one after a close, shows nothing.
func TestALatePanelSizeShowsNothing(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := dockedRig(t)
		r.host.handlers().OnFloat(panelMsg(FloatMessage{Kind: FloatSize, W: 476, H: 600, Mode: ModePanel}))
		want(t, r.host.taken())
	})
}

// A peek the page turns into the full panel is the full panel for the
// controller too: a click on the tab closes it, and an arrival peeks at
// nothing.
func TestAPeekThePageOpenedIntoThePanelClosesOnAToggle(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := dockedRig(t)
		r.host.handlers().OnFloat(FloatMessage{Window: FloatTab, Kind: FloatPeek, Item: "gate:7"})
		r.host.handlers().OnFloat(panelMsg(FloatMessage{Kind: FloatClose}))
		r.host.handlers().OnFloat(panelMsg(FloatMessage{Kind: FloatSize, W: 356, H: 60, Mode: ModePeek}))
		r.host.handlers().OnFloat(panelMsg(FloatMessage{Kind: FloatSize, W: 476, H: 600, Mode: ModePanel}))
		r.host.taken()
		r.host.handlers().OnFloat(FloatMessage{Window: FloatTab, Kind: FloatPeek, Item: "gate:8"})
		want(t, r.host.taken())
		r.host.handlers().OnFloat(FloatMessage{Window: FloatTab, Kind: FloatOpenPanel, Toggle: true})
		want(t, r.host.taken(), "fpanel-hide", `fpanel-tell "" "" "" 0x0`)
	})
}

// A peek's size that lands after the full panel was asked is late: the
// window stays hidden and unkeyable until the page draws the full panel, and
// a click on the tab still closes it.
func TestALatePeekSizeNeverTurnsAnAskedPanelBackIntoAPeek(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := dockedRig(t)
		r.host.handlers().OnFloat(FloatMessage{Window: FloatTab, Kind: FloatPeek, Item: "gate:7"})
		r.host.handlers().OnFloat(panelMsg(FloatMessage{Kind: FloatClose}))
		r.host.handlers().OnFloat(FloatMessage{Window: FloatTab, Kind: FloatOpenPanel})
		r.host.taken()
		r.host.handlers().OnFloat(panelMsg(FloatMessage{Kind: FloatSize, W: 356, H: 60, Mode: ModePeek}))
		want(t, r.host.taken())
		r.host.handlers().OnFloat(panelMsg(FloatMessage{Kind: FloatSize, W: 476, H: 600, Mode: ModePanel}))
		want(t, r.host.taken(), "fpanel-show {1002 260.75 476 600} panel")
		r.host.handlers().OnFloat(FloatMessage{Window: FloatTab, Kind: FloatOpenPanel, Toggle: true})
		want(t, r.host.taken(), "fpanel-hide", `fpanel-tell "" "" "" 0x0`)
	})
}

// A full panel's size from before a close that lands after a new peek was
// asked is late too: only a page that drew the peek turns it into the panel.
func TestALatePanelSizeNeverShowsAPeekAsThePanel(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := dockedRig(t)
		r.host.handlers().OnFloat(FloatMessage{Window: FloatTab, Kind: FloatOpenPanel})
		r.host.handlers().OnFloat(panelMsg(FloatMessage{Kind: FloatClose}))
		r.host.handlers().OnFloat(panelMsg(FloatMessage{Kind: FloatSize, W: 476, H: 600, Mode: ModePanel}))
		r.host.handlers().OnFloat(FloatMessage{Window: FloatTab, Kind: FloatOpenPanel, Toggle: true})
		r.host.handlers().OnFloat(FloatMessage{Window: FloatTab, Kind: FloatPeek, Item: "gate:7"})
		r.host.taken()
		r.host.handlers().OnFloat(panelMsg(FloatMessage{Kind: FloatSize, W: 476, H: 600, Mode: ModePanel}))
		want(t, r.host.taken())
		r.host.handlers().OnFloat(panelMsg(FloatMessage{Kind: FloatSize, W: 356, H: 60, Mode: ModePeek}))
		want(t, r.host.taken(), "fpanel-show {1122 260.75 356 60} peek")
	})
}

// The window shows only at a size the page sent for what it was asked to
// show: a size from an earlier open never shows it, even as the tab moves.
func TestThePanelNeverShowsAtTheSizeOfAnEarlierOpen(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := dockedRig(t)
		r.host.handlers().OnFloat(FloatMessage{Window: FloatTab, Kind: FloatOpenPanel})
		r.host.handlers().OnFloat(panelMsg(FloatMessage{Kind: FloatClose}))
		r.host.handlers().OnFloat(panelMsg(FloatMessage{Kind: FloatSize, W: 476, H: 600, Mode: ModePanel}))
		r.host.handlers().OnFloat(panelMsg(FloatMessage{Kind: FloatClose}))
		r.host.handlers().OnFloat(FloatMessage{Window: FloatTab, Kind: FloatPeek, Item: "gate:7"})
		r.host.taken()
		r.host.handlers().OnFloat(size(34, 120))
		r.host.handlers().OnScreens([]Screen{laptop})
		for _, c := range r.host.taken() {
			if strings.HasPrefix(c, "fpanel-show") {
				t.Fatalf("showed the panel at an earlier size: %q", c)
			}
		}
	})
}

// The page closing on its own is told it is closed, so a show it took in the
// meantime does not leave it drawing in a hidden window.
func TestAPanelThatClosedIsToldSo(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := dockedRig(t)
		r.host.handlers().OnFloat(FloatMessage{Window: FloatTab, Kind: FloatOpenPanel})
		r.host.handlers().OnFloat(panelMsg(FloatMessage{Kind: FloatClose}))
		r.host.handlers().OnFloat(panelMsg(FloatMessage{Kind: FloatSize, W: 476, H: 600, Mode: ModePanel}))
		r.host.taken()
		r.host.handlers().OnFloat(panelMsg(FloatMessage{Kind: FloatClose}))
		want(t, r.host.taken(), "fpanel-hide", `fpanel-tell "" "" "" 0x0`)
	})
}

// No peek opens beside a tab being dragged.
func TestNoPeekOpensDuringADrag(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := dockedRig(t)
		r.host.handlers().OnFloatMoving()
		r.host.handlers().OnFloat(FloatMessage{Window: FloatTab, Kind: FloatPeek, Item: "gate:7"})
		r.host.handlers().OnFloat(FloatMessage{Window: FloatTab, Kind: FloatOpenPanel})
		want(t, r.host.taken())
	})
}

// Without screens the panel goes where the tab is told, and nothing panics.
func TestThePanelPlacesWithoutScreens(t *testing.T) {
	tab := Rect{X: 10, Y: 10, W: 112, H: 32}
	if side, _ := PanelRoom(tab, "", nil); side == "" {
		t.Fatal("no side without screens")
	}
	_ = PanelFrame(tab, "", 476, 600, nil)
}

// A shown panel without screens stays where it is.
func TestAPanelIsNotPlacedWithoutScreens(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := dockedRig(t)
		r.host.handlers().OnFloat(FloatMessage{Window: FloatTab, Kind: FloatOpenPanel})
		r.host.handlers().OnFloat(panelMsg(FloatMessage{Kind: FloatClose}))
		r.host.handlers().OnFloat(panelMsg(FloatMessage{Kind: FloatSize, W: 476, H: 600, Mode: ModePanel}))
		r.host.taken()
		r.host.handlers().OnScreens(nil)
		want(t, r.host.taken())
	})
}

// A float the controller reloads at a new address as its events come back
// carries nothing until the new page reports in, and its hook hears it.
func TestAFloatReloadedAtANewAddressTellsItsHook(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		port := "7000"
		first := make(chan api.Change)
		r := &rig{host: &fakeHost{}, events: &fakeEvents{streams: []chan api.Change{first, make(chan api.Change)}, stop: make(chan struct{})}, logs: &logs{}}
		var mu sync.Mutex
		url := func(route string) (string, error) {
			mu.Lock()
			defer mu.Unlock()
			return "http://127.0.0.1:" + port + "/#" + route, nil
		}
		r.ctl = New(Config{Host: r.host, Daemon: r.events, CockpitURL: url})
		r.run(t)
		synctest.Wait()
		r.host.handlers().OnScreens([]Screen{laptop})
		if err := r.ctl.ShowFloat(); err != nil {
			t.Fatal(err)
		}
		var changes atomic.Int32
		r.ctl.OnFloatReady(func() { changes.Add(1) })
		r.host.handlers().OnFloat(readState(ReadOK))
		mu.Lock()
		port = "7001"
		mu.Unlock()
		close(first)
		time.Sleep(time.Second)
		synctest.Wait()
		if r.ctl.FloatReady() || changes.Load() != 2 {
			t.Fatalf("ready %v, hook ran %d times; want the reloaded float not ready, its hook told", r.ctl.FloatReady(), changes.Load())
		}
	})
}

// Once Run returns, no check the controller armed tells its hook anything.
func TestAStoppedControllerTellsItsHookNothing(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := &rig{host: &fakeHost{}, events: &fakeEvents{streams: []chan api.Change{make(chan api.Change)}, stop: make(chan struct{})}, logs: &logs{}}
		r.ctl = New(Config{Host: r.host, Daemon: r.events, CockpitURL: cockpitURL})
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() { done <- r.ctl.Run(ctx) }()
		synctest.Wait()
		r.host.handlers().OnScreens([]Screen{laptop})
		if err := r.ctl.ShowFloat(); err != nil {
			t.Fatal(err)
		}
		var changes atomic.Int32
		r.ctl.OnFloatReady(func() { changes.Add(1) })
		r.host.handlers().OnFloat(readState(ReadOK))
		cancel()
		close(r.events.stop)
		<-done
		time.Sleep(FloatSilentAfter)
		synctest.Wait()
		if n := changes.Load(); n != 1 {
			t.Fatalf("hook ran %d times, want once, before Run returned", n)
		}
	})
}
