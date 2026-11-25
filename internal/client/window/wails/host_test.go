//go:build darwin && purego

package wails

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

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

// fakeFloat is the float's AppKit side: the mouse buttons, the frame, the
// key status and the hand-backs, each read under mu.
type fakeFloat struct {
	mu        sync.Mutex
	up, key   bool
	frame     window.Rect
	handBacks int
}

func (f *fakeFloat) set(fn func(f *fakeFloat)) { f.mu.Lock(); fn(f); f.mu.Unlock() }

func (f *fakeFloat) handedBack() int { f.mu.Lock(); defer f.mu.Unlock(); return f.handBacks }

func (f *fakeFloat) native() floatNative {
	read := func(fn func() any) any { f.mu.Lock(); defer f.mu.Unlock(); return fn() }
	return floatNative{
		buttonsUp: func() bool { return read(func() any { return f.up }).(bool) },
		frame:     func() window.Rect { return read(func() any { return f.frame }).(window.Rect) },
		isKey:     func() bool { return read(func() any { return f.key }).(bool) },
		handBack:  func() { f.set(func(f *fakeFloat) { f.handBacks++; f.key = false }) },
	}
}

// runningHost is a host whose worker runs on f until the test ends, then
// stops with every release poll awaited.
func runningHost(t *testing.T, f *fakeFloat, hs window.Handlers) *host {
	h := &host{native: f.native(), listening: hs}
	h.start()
	t.Cleanup(h.stop)
	return h
}

// A move made with every button up is AppKit's, not the person's; a move
// under a held button is a drag, dropped at the first poll that finds the
// buttons up, with the frame it was dropped at.
func TestADragIsFollowedUntilTheButtonsAreUp(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := &fakeFloat{up: true, frame: window.Rect{X: 600, Y: 300, W: 112, H: 32}}
		var mu sync.Mutex
		var moving int
		var dropped []window.Rect
		var droppedAt time.Time
		h := runningHost(t, f, window.Handlers{
			OnFloatMoving:  func() { mu.Lock(); moving++; mu.Unlock() },
			OnFloatDropped: func(r window.Rect) { mu.Lock(); dropped = append(dropped, r); droppedAt = time.Now(); mu.Unlock() },
		})
		h.do(func() { h.floatShown, h.floatFrame = true, window.Rect{X: 600, Y: 300, W: 112, H: 32} })

		f.set(func(f *fakeFloat) { f.frame = window.Rect{X: 0, Y: 33, W: 112, H: 32} })
		h.do(h.floatMoved)
		synctest.Wait()
		mu.Lock()
		if moving != 0 {
			t.Fatalf("a move with the buttons up started a drag")
		}
		mu.Unlock()

		f.set(func(f *fakeFloat) { f.up, f.frame = false, window.Rect{X: 650, Y: 320, W: 112, H: 32} })
		start := time.Now()
		h.do(h.floatMoved)
		h.do(h.floatMoved)
		time.Sleep(dropPoll + dropPoll/2)
		synctest.Wait()
		mu.Lock()
		if moving != 1 || len(dropped) != 0 {
			t.Fatalf("held: moving %d, dropped %v; want one drag, no drop", moving, dropped)
		}
		mu.Unlock()
		f.set(func(f *fakeFloat) { f.up, f.frame = true, window.Rect{X: 900, Y: 500, W: 112, H: 32} })
		time.Sleep(dropPoll)
		synctest.Wait()
		mu.Lock()
		defer mu.Unlock()
		if len(dropped) != 1 || dropped[0] != (window.Rect{X: 900, Y: 500, W: 112, H: 32}) {
			t.Fatalf("dropped %v, want the frame at the release", dropped)
		}
		if took := droppedAt.Sub(start); took != 2*dropPoll {
			t.Fatalf("dropped after %v, want %v", took, 2*dropPoll)
		}
	})
}

// Leaving the panel under a held button hands the keyboard back once the
// button is up, not under the press; back in the panel before then, it
// keeps it.
func TestTheKeyboardGoesBackOnceTheButtonsAreUp(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := &fakeFloat{up: false, key: true}
		h := runningHost(t, f, window.Handlers{})
		h.do(func() { h.setFloatMode(window.ModePill) })
		time.Sleep(3 * dropPoll)
		synctest.Wait()
		if f.handedBack() != 0 {
			t.Fatal("handed the keyboard back under a held button")
		}
		f.set(func(f *fakeFloat) { f.up = true })
		time.Sleep(dropPoll)
		synctest.Wait()
		if n := f.handedBack(); n != 1 {
			t.Fatalf("handed back %d times, want once", n)
		}

		f.set(func(f *fakeFloat) { f.up, f.key = false, true })
		h.do(func() { h.setFloatMode(window.ModePeek) })
		h.do(func() { h.setFloatMode(window.ModePanel) })
		synctest.Wait()
		f.set(func(f *fakeFloat) { f.up = true })
		time.Sleep(dropPoll)
		synctest.Wait()
		if n := f.handedBack(); n != 1 {
			t.Fatalf("handed back %d times after returning to the panel, want still once", n)
		}
	})
}

// Stopping the host ends a poll still waiting for a release.
func TestStoppingTheHostEndsAWaitingPoll(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := &fakeFloat{up: false, key: true}
		h := &host{native: f.native()}
		h.start()
		h.do(func() { h.setFloatMode(window.ModePill) })
		synctest.Wait()
		h.stop()
	})
}

// The pointer the host tracks reaches the page as DOM events at the point,
// with its state kept beside the bridge the brand names.
func TestThePointerGoesToThePageAtItsPoint(t *testing.T) {
	js := floatPointerJS(pointerMoved, 56, 16)
	bridge, _ := json.Marshal(window.FloatBridge)
	for _, want := range []string{`(` + string(bridge) + `,"moved",56,16)`, `bridge + "Pointer"`, "elementFromPoint", "pointerenter", "pointerleave", "pointermove"} {
		if !strings.Contains(js, want) {
			t.Errorf("js lacks %s:\n%s", want, js)
		}
	}
	if got := floatPointerJS(pointerExited, 0, 0); !strings.HasSuffix(got, `,"exited",0,0)`) {
		t.Errorf("exit js %s", got)
	}
}

type warnings []string

func (w *warnings) Warn(msg string, args ...any) {
	*w = append(*w, fmt.Sprint(append([]any{msg}, args...)...))
}

// A float with no window is logged, not left unready in silence.
func TestAFloatItCannotReadyIsLogged(t *testing.T) {
	var w warnings
	floatOnly(0, func() bool { return false }, &w)
	if len(w) != 1 || !strings.Contains(w[0], "no float window") {
		t.Fatalf("logged %q", w)
	}
}
