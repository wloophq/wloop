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

	"github.com/wailsapp/wails/v3/pkg/application"

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

const (
	cockpit      = "http://127.0.0.1:7000/?token=a#/float/tab"
	cockpitPanel = "http://127.0.0.1:7001/?token=b#/float/panel"
)

// Only the float's own pages, in their main frame and from the origin each
// window loaded, reach the controller, named by the window they came from.
func TestOnlyTheCockpitsPagesSendFloatMessages(t *testing.T) {
	open := `{"type":"float:open","route":"task=t-1"}`
	for _, c := range []struct {
		window, origin string
		main           bool
		msg            string
		from           string
	}{
		{floatName, "http://127.0.0.1:7000/?token=a#/float/tab", true, open, window.FloatTab},
		{floatName, "http://127.0.0.1:7000/", true, open, window.FloatTab},
		{floatName, "http://127.0.0.1:7001/", true, open, ""},
		{floatPanelName, "http://127.0.0.1:7001/", true, open, window.FloatPanel},
		{floatPanelName, "http://127.0.0.1:7000/", true, open, ""},
		{floatName, "https://elsewhere.example/", true, open, ""},
		{floatName, "", true, open, ""},
		{floatName, "http://127.0.0.1:7000/", false, open, ""},
		{"panel", "http://127.0.0.1:7000/", true, open, ""},
		{floatName, "http://127.0.0.1:7000/", true, `float:open {"route":"task=t-1"}`, ""},
	} {
		var got []window.FloatMessage
		h := &host{floatURL: cockpit, floatPanelURL: cockpitPanel, listening: window.Handlers{OnFloat: func(m window.FloatMessage) { got = append(got, m) }}}
		h.pageMessage(c.window, c.msg, c.origin, c.main)
		for _, fn := range h.queue {
			fn()
		}
		from := ""
		if len(got) == 1 && got[0].Kind == window.FloatOpen && got[0].Route == "task=t-1" {
			from = got[0].Window
		}
		if len(got) > 1 || from != c.from {
			t.Errorf("%s from %q main=%v %q: delivered %+v, want from %q", c.window, c.origin, c.main, c.msg, got, c.from)
		}
	}
}

// The host calls each page's global the brand names: the tab's place, and
// what the panel shows, that it should leave, or that it lost the keyboard.
func TestTheHostCallsTheBrandsGlobal(t *testing.T) {
	name, _ := json.Marshal(window.FloatBridge)
	call := func(method, args string) string {
		return `(function(b){if(b&&b.` + method + `)b.` + method + `(` + args + `)})(window[` + string(name) + `])`
	}
	for _, c := range []struct{ got, want string }{
		{floatPlaceJS(window.FloatPlace{Edge: window.EdgeLeft}), call("place", `{"edge":"left"}`)},
		{panelShowJS(window.PanelPlace{Mode: window.ModePeek, Item: "gate:7", Side: window.SideLeft, MaxW: 1478, MaxH: 911}),
			call("show", `"peek",{"item":"gate:7","side":"left","maxW":1478,"maxH":911}`)},
		{panelShowJS(window.PanelPlace{}), call("show", `"",{"item":"","side":"","maxW":0,"maxH":0}`)},
		{bridgeJS("leave"), call("leave", "")},
		{bridgeJS("blur"), call("blur", "")},
	} {
		if c.got != c.want {
			t.Errorf("js\n got %s\nwant %s", c.got, c.want)
		}
	}
}

// fakeFloat is a float window's AppKit side: the mouse buttons, the frame,
// and what the host did to it, each read under mu.
type fakeFloat struct {
	mu     sync.Mutex
	up     bool
	frame  window.Rect
	ran    []string
	did    []string
	frames []window.Rect
}

func (f *fakeFloat) set(fn func(f *fakeFloat)) { f.mu.Lock(); fn(f); f.mu.Unlock() }

func (f *fakeFloat) taken() (ran, did []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	ran, did = f.ran, f.did
	f.ran, f.did = nil, nil
	return ran, did
}

func (f *fakeFloat) native() floatNative {
	read := func(fn func() any) any { f.mu.Lock(); defer f.mu.Unlock(); return fn() }
	return floatNative{
		buttonsUp: func() bool { return read(func() any { return f.up }).(bool) },
		frame:     func() window.Rect { return read(func() any { return f.frame }).(window.Rect) },
		run:       func(js string) { f.set(func(f *fakeFloat) { f.ran = append(f.ran, js) }) },
		load:      func(url string) { f.set(func(f *fakeFloat) { f.did = append(f.did, "load "+url) }) },
		show:      func() { f.set(func(f *fakeFloat) { f.did = append(f.did, "show") }) },
		hide:      func() { f.set(func(f *fakeFloat) { f.did = append(f.did, "hide") }) },
		setFrame:  func(r window.Rect) { f.set(func(f *fakeFloat) { f.frames = append(f.frames, r) }) },
	}
}

// runningHost is a host whose worker runs on the tab f until the test ends,
// then stops with every poll and grace awaited; its panel window is panel,
// made on first use.
func runningHost(t *testing.T, f, panel *fakeFloat, hs window.Handlers) *host {
	h := &host{native: f.native(), listening: hs}
	h.makePanel = panel.native
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
		h := runningHost(t, f, &fakeFloat{}, window.Handlers{
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

// Stopping the host ends a poll still waiting for a release and a grace
// still running.
func TestStoppingTheHostEndsAWaitingPoll(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := &fakeFloat{up: false, frame: window.Rect{X: 9, Y: 9, W: 34, H: 84}}
		h := &host{native: f.native()}
		h.makePanel = (&fakeFloat{}).native
		h.start()
		h.LoadFloatPanel(cockpitPanel)
		h.do(func() { h.floatShown = true })
		h.do(h.floatMoved)
		h.do(func() {
			h.showPanel(window.Rect{W: 1, H: 1}, window.ModePanel)
		})
		h.do(func() { h.pointer(window.FloatTab, false) })
		synctest.Wait()
		h.stop()
	})
}

// The panel window is made on the first load only and loads each URL it is
// given; showing it frames it first. Before the first load, nothing is made.
func TestThePanelWindowIsMadeOnItsFirstLoad(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		panel := &fakeFloat{up: true}
		made := 0
		h := runningHost(t, &fakeFloat{up: true}, panel, window.Handlers{})
		h.makePanel = func() floatNative { made++; return panel.native() }
		h.ShowFloatPanel(window.Rect{W: 1, H: 1}, window.ModePanel)
		h.HideFloatPanel()
		h.LoadFloatPanel(cockpitPanel)
		h.LoadFloatPanel(cockpitPanel)
		h.ShowFloatPanel(window.Rect{X: 1002, Y: 260.75, W: 476, H: 600}, window.ModePanel)
		h.HideFloat()
		synctest.Wait()
		_, did := panel.taken()
		if made != 1 || !slices.Equal(did, []string{"load " + cockpitPanel, "load " + cockpitPanel, "show", "hide"}) {
			t.Fatalf("made %d, did %q; want one window that loads twice, shows and hides", made, did)
		}
		if len(panel.frames) != 1 || panel.frames[0] != (window.Rect{X: 1002, Y: 260.75, W: 476, H: 600}) {
			t.Fatalf("frames %v", panel.frames)
		}
	})
}

// Only the full panel may take the keyboard; a peek and a hidden panel never
// do, and a size the panel's page sends never makes its window keyable: only
// a show of the controller's does.
func TestOnlyTheFullPanelMayBecomeKey(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := runningHost(t, &fakeFloat{up: true}, &fakeFloat{up: true}, window.Handlers{})
		h.LoadFloatPanel(cockpitPanel)
		keyable := func(step string, want bool) {
			t.Helper()
			synctest.Wait()
			if got := h.panelKeyable.Load(); got != want {
				t.Fatalf("after %s keyable %v, want %v", step, got, want)
			}
		}
		h.ShowFloatPanel(window.Rect{W: 356, H: 60}, window.ModePeek)
		keyable("a peek", false)
		h.ShowFloatPanel(window.Rect{W: 476, H: 600}, window.ModePanel)
		keyable("the full panel", true)
		h.HideFloatPanel()
		keyable("a hide", false)
		h.pageMessage(floatPanelName, `{"type":"float:size","w":476,"h":600,"mode":"panel"}`, cockpitPanel, true)
		keyable("a late size of the page", false)
	})
}

// The pointer leaving both windows asks an open panel's page to leave once
// floatGrace has passed; entering either window before then keeps it, and a
// peek is never asked.
func TestLeavingBothWindowsAsksThePanelToLeaveAfterTheGrace(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		panel := &fakeFloat{up: true}
		h := runningHost(t, &fakeFloat{up: true}, panel, window.Handlers{})
		h.LoadFloatPanel(cockpitPanel)
		mode := func(m string) {
			h.do(func() { h.showPanel(window.Rect{W: 476, H: 600}, m) })
		}
		pointer := func(w string, in bool) { h.do(func() { h.pointer(w, in) }) }
		leaves := func() int {
			synctest.Wait()
			ran, _ := panel.taken()
			n := 0
			for _, js := range ran {
				if js == bridgeJS("leave") {
					n++
				}
			}
			return n
		}

		mode(window.ModePanel)
		pointer(window.FloatTab, true)
		pointer(window.FloatTab, false)
		time.Sleep(floatGrace / 2)
		pointer(window.FloatPanel, true)
		time.Sleep(floatGrace)
		if n := leaves(); n != 0 {
			t.Fatalf("crossing from the tab to the panel asked %d leaves", n)
		}
		pointer(window.FloatPanel, false)
		synctest.Wait()
		start := time.Now()
		time.Sleep(floatGrace - time.Millisecond)
		if n := leaves(); n != 0 {
			t.Fatal("asked to leave before the grace passed")
		}
		time.Sleep(time.Millisecond)
		if n := leaves(); n != 1 || time.Since(start) != floatGrace {
			t.Fatalf("asked %d leaves after %v, want one after %v", n, time.Since(start), floatGrace)
		}

		mode(window.ModePeek)
		pointer(window.FloatTab, true)
		pointer(window.FloatTab, false)
		time.Sleep(2 * floatGrace)
		if n := leaves(); n != 0 {
			t.Fatal("a peek was asked to leave")
		}
	})
}

// A full panel that shows after the pointer already left both windows, as
// when the pointer left the tab before the panel's page drew, is asked to
// leave after the grace too.
func TestAPanelThatShowsWithThePointerGoneIsAskedToLeave(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		panel := &fakeFloat{up: true}
		h := runningHost(t, &fakeFloat{up: true}, panel, window.Handlers{})
		h.LoadFloatPanel(cockpitPanel)
		h.do(func() { h.pointer(window.FloatTab, true) })
		h.do(func() { h.pointer(window.FloatTab, false) })
		h.do(func() {
			h.showPanel(window.Rect{W: 476, H: 600}, window.ModePanel)
		})
		synctest.Wait()
		time.Sleep(floatGrace)
		synctest.Wait()
		if ran, _ := panel.taken(); !slices.Contains(ran, bridgeJS("leave")) {
			t.Fatalf("ran %q, want a leave", ran)
		}
		h.do(func() { h.pointer(window.FloatTab, true) })
		h.do(func() {
			h.showPanel(window.Rect{W: 476, H: 640}, window.ModePanel)
		})
		time.Sleep(2 * floatGrace)
		synctest.Wait()
		if ran, _ := panel.taken(); slices.Contains(ran, bridgeJS("leave")) {
			t.Fatal("asked to leave with the pointer on the tab")
		}
	})
}

// The panel window resigning key tells the full panel's page, which closes
// unless pinned; a peek, never key, is told nothing.
func TestTheOpenPanelHearsItsWindowResignKey(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		panel := &fakeFloat{up: true}
		h := runningHost(t, &fakeFloat{}, panel, window.Handlers{})
		h.LoadFloatPanel(cockpitPanel)
		h.do(func() { h.showPanel(window.Rect{W: 1, H: 1}, window.ModePeek) })
		h.do(h.panelResigned)
		h.do(func() {
			h.showPanel(window.Rect{W: 1, H: 1}, window.ModePanel)
		})
		h.do(h.panelResigned)
		synctest.Wait()
		if ran, _ := panel.taken(); len(ran) != 1 || ran[0] != bridgeJS("blur") {
			t.Fatalf("ran %q, want once %s", ran, bridgeJS("blur"))
		}
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
	floatOnly(0, func() bool { return false }, func(bool) {}, &w)
	if len(w) != 1 || !strings.Contains(w[0], "no float window") {
		t.Fatalf("logged %q", w)
	}
}

// While the float is key WebKit gives the page the pointer, so the host
// dispatches nothing, but an exit then clears the element it last tracked:
// a later entry onto that element enters it again.
func TestAKeyFloatOnlyResetsThePointerOnExit(t *testing.T) {
	for _, c := range []struct {
		kind string
		key  bool
		want string
	}{
		{pointerEntered, false, pointerEntered},
		{pointerMoved, false, pointerMoved},
		{pointerExited, false, pointerExited},
		{pointerEntered, true, ""},
		{pointerMoved, true, ""},
		{pointerExited, true, pointerReset},
	} {
		if got := pointerKind(c.kind, c.key); got != c.want {
			t.Errorf("%s key=%v: %q, want %q", c.kind, c.key, got, c.want)
		}
	}
	if js := floatPointerJS(pointerReset, 0, 0); !strings.Contains(js, `kind === "reset"`) {
		t.Errorf("the script ignores a reset:\n%s", js)
	}
}

// Wails re-applies AlwaysOnTop as the level of a window created hidden when
// it first becomes key, so the floating panels ask for it too: false would
// drop them to the normal level, under other apps' windows.
func TestThePanelsStayFloatingOnceTheyBecomeKey(t *testing.T) {
	for _, o := range []application.WebviewWindowOptions{panelOptions("App"), floatOptions("App", floatName), floatOptions("App", floatPanelName)} {
		if !o.Hidden || !o.AlwaysOnTop || o.Mac.WindowLevel != application.MacWindowLevelFloating || o.Mac.WindowClass != application.MacWindowClassPanel {
			t.Errorf("%s: hidden %v, always on top %v, level %q, class %v; want a hidden floating panel kept on top",
				o.Name, o.Hidden, o.AlwaysOnTop, o.Mac.WindowLevel, o.Mac.WindowClass)
		}
		if o.Mac.CollectionBehavior&application.MacWindowCollectionBehaviorCanJoinAllSpaces == 0 {
			t.Errorf("%s: collection behavior %b, want it on every space", o.Name, o.Mac.CollectionBehavior)
		}
	}
	if panelOptions("App").Mac.CollectionBehavior&application.MacWindowCollectionBehaviorFullScreenAuxiliary == 0 {
		t.Errorf("the panel does not show over a full-screen app")
	}
}
