package window

import (
	"strings"
	"testing"
	"testing/synctest"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/brand"
)

// The visible frames of a laptop screen under its menu bar and of a larger
// screen to its right, in the coordinates of a Rect.
var (
	laptop = Rect{X: 0, Y: 33, W: 1512, H: 911}
	wide   = Rect{X: 1512, Y: 0, W: 2560, H: 1415}
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
	wantRect(t, ClampToScreens(onWide, []Rect{laptop, wide}), Rect{X: 3900 - 112, Y: 1300 - 32, W: 112, H: 32})
	wantRect(t, ClampToScreens(onWide, []Rect{laptop}), Rect{X: 1512 - 112, Y: 33 + 911 - 32, W: 112, H: 32})
}

// A docked pill stays flush with its edge, wherever its saved point drifted,
// and inside the screen's height.
func TestClampToScreensKeepsEdgeDock(t *testing.T) {
	right := FloatPlacement{Edge: EdgeRight, Corner: CornerTopRight, X: 4050, Y: 300, W: 34, H: 84}
	wantRect(t, ClampToScreens(right, []Rect{laptop, wide}), Rect{X: 1512 + 2560 - 34, Y: 300, W: 34, H: 84})

	left := FloatPlacement{Edge: EdgeLeft, Corner: CornerBottomLeft, X: 40, Y: 2000, W: 34, H: 84}
	wantRect(t, ClampToScreens(left, []Rect{laptop, wide}), Rect{X: 0, Y: 33 + 911 - 84, W: 34, H: 84})
}

// A frame larger than the screen starts at the screen's top left rather
// than past its far edges.
func TestClampToScreensKeepsAnOversizedFrameAtTheTopLeft(t *testing.T) {
	big := FloatPlacement{Corner: CornerBottomRight, X: 1500, Y: 900, W: 2000, H: 1200}
	wantRect(t, ClampToScreens(big, []Rect{laptop}), Rect{X: 0, Y: 33, W: 2000, H: 1200})
}

func TestPlacementOfAMovedFrame(t *testing.T) {
	screens := []Rect{laptop, wide}
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
	screens := []Rect{laptop, wide}
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
		{`float:size {"w":460,"h":600}`, FloatMessage{Kind: FloatSize, W: 460, H: 600}, true},
		{`float:size {"w":112,"h":32,"mode":"pill"}`, FloatMessage{Kind: FloatSize, W: 112, H: 32, Mode: ModePill}, true},
		{`float:size:{"w":112,"h":32}`, FloatMessage{Kind: FloatSize, W: 112, H: 32}, true},
		{`float:open {"route":"task=t-1"}`, FloatMessage{Kind: FloatOpen, Route: "task=t-1"}, true},
		{`float:hide {"mode":"quiet"}`, FloatMessage{Kind: FloatHide, Mode: "quiet"}, true},
		{`float:size {"w":0,"h":32}`, FloatMessage{}, false},
		{`float:size`, FloatMessage{}, false},
		{`float:sizes {"w":1,"h":1}`, FloatMessage{}, false},
		{`float:move {"x":1}`, FloatMessage{}, false},
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
	for mode, want := range map[string]bool{ModePill: false, ModePeek: false, ModePanel: true, "": true} {
		if got := FloatKeyable(mode); got != want {
			t.Errorf("mode %q keyable %v, want %v", mode, got, want)
		}
	}
}

// floatRig is a running controller whose host listened and reported screens.
func floatRig(t *testing.T, d Defaults, screens ...Rect) *rig {
	t.Helper()
	r := &rig{host: &fakeHost{}, events: &fakeEvents{streams: []chan api.Change{make(chan api.Change)}, stop: make(chan struct{})}, logs: &logs{}}
	r.ctl = New(Config{Host: r.host, Daemon: r.events, CockpitURL: cockpitURL, Log: r.logs.logf, Defaults: d})
	r.run(t)
	synctest.Wait()
	r.host.handlers().OnScreens(screens)
	return r
}

// Growing from the bottom right, the frame keeps its right and bottom edges
// where they were: the pill stays under the mouse while the panel opens.
func TestFloatSizeKeepsAnchorCorner(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		d := memDefaults{}
		SavePlacement(d, FloatPlacement{Corner: CornerBottomRight, X: 1400, Y: 900, W: 112, H: 32})
		r := floatRig(t, d, laptop)
		if err := r.ctl.ShowFloat(); err != nil {
			t.Fatal(err)
		}
		want(t, r.host.taken(), "float-frame {1288 868 112 32}", "float "+base+"#"+FloatRoute)
		r.host.handlers().OnFloat(FloatMessage{Kind: FloatSize, W: 460, H: 600})
		r.host.handlers().OnFloat(FloatMessage{Kind: FloatSize, W: 112, H: 32})
		want(t, r.host.taken(), "float-frame {940 300 460 600}", "float-frame {1288 868 112 32}")
	})
}

// A size the page sends while the float is hidden moves nothing.
func TestFloatSizeWhileHiddenMovesNothing(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := floatRig(t, nil, laptop)
		r.host.handlers().OnFloat(FloatMessage{Kind: FloatSize, W: 460, H: 600})
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
	})
}

// Unplugging the screen the pill sits on brings it back on the main screen;
// plugging it back returns the pill where the person left it.
func TestScreensChangingMoveTheFloatBack(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		d := memDefaults{}
		SavePlacement(d, FloatPlacement{Corner: CornerBottomRight, X: 3900, Y: 1300, W: 112, H: 32})
		r := floatRig(t, d, laptop, wide)
		if err := r.ctl.ShowFloat(); err != nil {
			t.Fatal(err)
		}
		r.host.taken()
		r.host.handlers().OnScreens([]Rect{laptop})
		r.host.handlers().OnScreens([]Rect{laptop, wide})
		want(t, r.host.taken(), "float-frame {1400 912 112 32}", "float-frame {3788 1268 112 32}")
	})
}

// A move the person made is saved, and the next size snaps the frame to the
// edge it was dropped near.
func TestAMoveIsSavedAndSnapsOnTheNextSize(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		d := memDefaults{}
		r := floatRig(t, d, laptop)
		if err := r.ctl.ShowFloat(); err != nil {
			t.Fatal(err)
		}
		r.host.taken()
		r.host.handlers().OnFloatMoved(Rect{X: 10, Y: 500, W: 112, H: 32})
		saved, ok := LoadPlacement(d)
		if want := (FloatPlacement{Edge: EdgeLeft, Corner: CornerBottomLeft, X: 0, Y: 532, W: 112, H: 32}); !ok || saved != want {
			t.Fatalf("saved %+v %v, want %+v", saved, ok, want)
		}
		want(t, r.host.taken())
		r.host.handlers().OnFloat(FloatMessage{Kind: FloatSize, W: 34, H: 84})
		want(t, r.host.taken(), "float-frame {0 448 34 84}")
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
		r := floatRig(t, nil, laptop)
		if err := r.ctl.ShowFloat(); err != nil {
			t.Fatal(err)
		}
		r.host.taken()
		r.ctl.HideFloat()
		r.host.handlers().OnFloat(FloatMessage{Kind: FloatSize, W: 460, H: 600})
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
