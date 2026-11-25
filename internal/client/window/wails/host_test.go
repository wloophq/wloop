//go:build darwin && purego

package wails

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/client/window"
)

var _ window.Host = (*host)(nil)

// A link that launched the app arrives before the controller listens: it is
// kept and handed over once a handler is bound, in order.
func TestALinkBeforeListenIsHandedOverOnListen(t *testing.T) {
	links := []string{brand.URLScheme() + "://open/#task=t-1", brand.URLScheme() + "://open/#task=t-2", brand.URLScheme() + "://open/#task=t-3"}
	h := &host{}
	h.openURL(links[0])
	h.openURL(links[1])
	var got []string
	if err := h.Listen(window.Handlers{OnOpenURL: func(l string) { got = append(got, l) }}); err != nil {
		t.Fatal(err)
	}
	h.openURL(links[2])
	if !slices.Equal(got, links) {
		t.Fatalf("handed over %q, want %q", got, links)
	}
}

// The panel opens in the middle of the focused screen's visible area, which
// on a second screen starts away from the origin.
func TestThePanelCentersInTheFocusedScreen(t *testing.T) {
	right := rect{Origin: point{X: 1512, Y: -200}, Size: size{Width: 2560, Height: 1415}}
	if got, want := centeredIn(right, size{Width: 680, Height: 420}), (point{X: 1512 + 940, Y: -200 + 497.5}); got != want {
		t.Fatalf("origin %+v, want %+v", got, want)
	}
}

var _ window.Defaults = (*host)(nil)

// A screen above the main one starts at a negative Y in AppKit's frame and
// round-trips through window.Rect, whose Y grows down from the main
// screen's top.
func TestFramesConvertBetweenAppKitAndTopLeft(t *testing.T) {
	const mainHeight = 982
	above := rect{Origin: point{X: -200, Y: 982}, Size: size{Width: 1920, Height: 1055}}
	got := topLeft(above, mainHeight)
	if want := (window.Rect{X: -200, Y: -1055, W: 1920, H: 1055}); got != want {
		t.Fatalf("top-left %+v, want %+v", got, want)
	}
	if back := bottomLeft(got, mainHeight); back != above {
		t.Fatalf("back %+v, want %+v", back, above)
	}
	pill := window.Rect{X: 1400, Y: 900, W: 112, H: 32}
	if got := bottomLeft(pill, mainHeight); got != (rect{Origin: point{X: 1400, Y: 50}, Size: size{Width: 112, Height: 32}}) {
		t.Fatalf("bottom-left %+v", got)
	}
}

// AppKit rounds a frame it is given; that frame is the one the host set, not
// a move the person made.
func TestARoundedFrameIsTheOneSet(t *testing.T) {
	set := window.Rect{X: 1400.25, Y: 260.75, W: 112, H: 32}
	if !sameFrame(window.Rect{X: 1400, Y: 261, W: 112, H: 32}, set) {
		t.Fatal("a rounded frame read as moved")
	}
	if sameFrame(window.Rect{X: 1380, Y: 261, W: 112, H: 32}, set) {
		t.Fatal("a moved frame read as the one set")
	}
}

const cockpit = "http://127.0.0.1:7000/?token=a#/float"

// Only the float's own page, in its main frame and from the cockpit's
// origin, reaches the controller.
func TestOnlyTheCockpitsPageSendsFloatMessages(t *testing.T) {
	open := `{"type":"float:open","route":"task=t-1"}`
	for _, c := range []struct {
		window, origin string
		main           bool
		msg            string
		delivered      bool
	}{
		{floatName, "http://127.0.0.1:7000/?token=a#/float", true, open, true},
		{floatName, "http://127.0.0.1:7000/", true, open, true},
		{floatName, "http://127.0.0.1:7001/", true, open, false},
		{floatName, "https://elsewhere.example/", true, open, false},
		{floatName, "", true, open, false},
		{floatName, "http://127.0.0.1:7000/", false, open, false},
		{"panel", "http://127.0.0.1:7000/", true, open, false},
		{floatName, "http://127.0.0.1:7000/", true, `float:open {"route":"task=t-1"}`, false},
	} {
		var got []window.FloatMessage
		h := &host{floatURL: cockpit, listening: window.Handlers{OnFloat: func(m window.FloatMessage) { got = append(got, m) }}}
		h.pageMessage(c.window, c.msg, c.origin, c.main)
		for _, fn := range h.queue {
			fn()
		}
		if delivered := len(got) == 1 && got[0] == (window.FloatMessage{Kind: window.FloatOpen, Route: "task=t-1"}); delivered != c.delivered {
			t.Errorf("%s from %q main=%v %q: delivered %v, want %v", c.window, c.origin, c.main, c.msg, delivered, c.delivered)
		}
	}
}

// The host calls the page's global the brand names, with the mode the page
// last said it draws in, the pill until it says one.
func TestThePlaceGoesToTheBrandsGlobal(t *testing.T) {
	name, _ := json.Marshal(window.FloatBridge)
	want := `(function(b){if(b&&b.setMode)b.setMode("pill",{"edge":"left","up":true})})(window[` + string(name) + `])`
	if got := floatPlaceJS("", window.FloatPlace{Edge: window.EdgeLeft, Up: true}); got != want {
		t.Fatalf("js\n got %s\nwant %s", got, want)
	}
	if got := floatPlaceJS(window.ModePanel, window.FloatPlace{}); !strings.Contains(got, `setMode("panel",{"edge":"","up":false})`) {
		t.Fatalf("js %s", got)
	}
}
