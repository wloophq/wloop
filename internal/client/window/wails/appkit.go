//go:build darwin && purego

package wails

import (
	_ "embed"
	"encoding/json"
	"runtime"
	"strconv"
	"sync"

	"github.com/ebitengine/purego"
	"github.com/ebitengine/purego/objc"
	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/client/window"
)

var sel = objc.RegisterName

func class(name string) objc.ID { return objc.ID(objc.GetClass(name)) }

func nsApp() objc.ID { return class("NSApplication").Send(sel("sharedApplication")) }

func nsString(s string) objc.ID {
	b := append([]byte(s), 0)
	return class("NSString").Send(sel("stringWithUTF8String:"), &b[0])
}

func nsWindow(w *application.WebviewWindow) objc.ID { return objc.ID(uintptr(w.NativeWindow())) }

// setActivationPolicy puts the app in the Dock and ⌘Tab (regular) or out of
// them (accessory); Wails sets the policy only at launch.
func setActivationPolicy(p int) {
	application.InvokeSync(func() { nsApp().Send(sel("setActivationPolicy:"), p) })
}

func isKey(w *application.WebviewWindow) (key bool) {
	application.InvokeSync(func() { key = objc.Send[bool](nsWindow(w), sel("isKeyWindow")) })
	return key
}

// evalJS runs js in w's page. Wails' ExecJS waits for its own runtime, which
// a page it does not serve never loads.
func evalJS(w *application.WebviewWindow, js string) {
	application.InvokeSync(func() {
		if wv := findWebView(nsWindow(w).Send(sel("contentView"))); wv != 0 {
			wv.Send(sel("evaluateJavaScript:completionHandler:"), nsString(js), objc.ID(0))
		}
	})
}

func findWebView(v objc.ID) objc.ID {
	if v == 0 {
		return 0
	}
	if objc.Send[bool](v, sel("isKindOfClass:"), class("WKWebView")) {
		return v
	}
	subs := v.Send(sel("subviews"))
	for i := range objc.Send[int](subs, sel("count")) {
		if wv := findWebView(subs.Send(sel("objectAtIndex:"), i)); wv != 0 {
			return wv
		}
	}
	return 0
}

type point struct{ X, Y float64 }
type size struct{ Width, Height float64 }
type rect struct {
	Origin point
	Size   size
}

// centeredIn is the origin that centers a window of size s in area, in
// AppKit's bottom-left coordinates.
func centeredIn(area rect, s size) point {
	return point{X: area.Origin.X + (area.Size.Width-s.Width)/2, Y: area.Origin.Y + (area.Size.Height-s.Height)/2}
}

// centerOnFocusedScreen moves w to the middle of the screen whose window has
// the keyboard focus, whichever app owns it, unless w is already on it, where
// it stays where it was left; it runs on the main thread.
func centerOnFocusedScreen(w *application.WebviewWindow) {
	screen := class("NSScreen").Send(sel("mainScreen"))
	win := nsWindow(w)
	if screen == 0 || objc.Send[bool](screen, sel("isEqual:"), win.Send(sel("screen"))) {
		return
	}
	area := objc.Send[rect](screen, sel("visibleFrame"))
	frame := objc.Send[rect](win, sel("frame"))
	win.Send(sel("setFrameOrigin:"), centeredIn(area, frame.Size))
}

// topLeft is the window.Rect of r, an AppKit frame whose origin is the
// bottom left of the main screen, mainHeight tall.
func topLeft(r rect, mainHeight float64) window.Rect {
	return window.Rect{X: r.Origin.X, Y: mainHeight - r.Origin.Y - r.Size.Height, W: r.Size.Width, H: r.Size.Height}
}

// bottomLeft is the AppKit frame of r.
func bottomLeft(r window.Rect, mainHeight float64) rect {
	return rect{Origin: point{X: r.X, Y: mainHeight - r.Y - r.H}, Size: size{Width: r.W, Height: r.H}}
}

// screens is every screen with its visible frame, without the menu bar and
// the Dock, the main screen first; it runs on the main thread.
func screens() []window.Screen {
	all := class("NSScreen").Send(sel("screens"))
	n := objc.Send[int](all, sel("count"))
	if n == 0 {
		return nil
	}
	mainHeight := objc.Send[rect](all.Send(sel("objectAtIndex:"), 0), sel("frame")).Size.Height
	out := make([]window.Screen, 0, n)
	for i := range n {
		s := all.Send(sel("objectAtIndex:"), i)
		out = append(out, window.Screen{
			Frame:   topLeft(objc.Send[rect](s, sel("frame")), mainHeight),
			Visible: topLeft(objc.Send[rect](s, sel("visibleFrame")), mainHeight),
		})
	}
	return out
}

func mainScreenHeight() float64 {
	main := class("NSScreen").Send(sel("screens")).Send(sel("firstObject"))
	if main == 0 {
		return 0
	}
	return objc.Send[rect](main, sel("frame")).Size.Height
}

// frameOf is win's frame; it runs on the main thread.
func frameOf(win objc.ID) window.Rect {
	return topLeft(objc.Send[rect](win, sel("frame")), mainScreenHeight())
}

// setFrame moves and sizes win to r at once, without animating; it runs on
// the main thread.
func setFrame(win objc.ID, r window.Rect) {
	win.Send(sel("setFrame:display:animate:"), bottomLeft(r, mainScreenHeight()), true, false)
}

// addCollectionBehavior adds b to how win joins spaces and full-screen apps;
// it runs on the main thread.
func addCollectionBehavior(win objc.ID, b application.MacWindowCollectionBehavior) {
	current := objc.Send[uint](win, sel("collectionBehavior"))
	win.Send(sel("setCollectionBehavior:"), current|uint(b))
}

// handBackKeyboard gives the keyboard back to the app the person was typing
// in: a non-activating panel ordered out passes key status back to the
// active app's window. It runs on the main thread, with no mouse button
// down, since ordering out under a press drops its mouse-up.
func handBackKeyboard(win objc.ID) {
	win.Send(sel("orderOut:"), objc.ID(0))
	win.Send(sel("orderFrontRegardless"))
}

func mouseButtonsUp() bool {
	return objc.Send[uint](objc.ID(class("NSEvent")), sel("pressedMouseButtons")) == 0
}

// The NSTrackingArea options that report the mouse over a window that is
// not key: entered and exited, moved, always active, over the visible rect.
const floatTracking = 0x01 | 0x02 | 0x80 | 0x200

var objcRuntime struct {
	once          sync.Once
	err           error
	replaceMethod func(cls objc.Class, name objc.SEL, imp objc.IMP, types string) objc.IMP
	methodIMP     func(cls objc.Class, name objc.SEL) objc.IMP
}

func loadObjcRuntime() error {
	objcRuntime.once.Do(func() {
		lib, err := purego.Dlopen("/usr/lib/libobjc.A.dylib", purego.RTLD_NOW|purego.RTLD_GLOBAL)
		if err != nil {
			objcRuntime.err = err
			return
		}
		purego.RegisterLibFunc(&objcRuntime.replaceMethod, lib, "class_replaceMethod")
		purego.RegisterLibFunc(&objcRuntime.methodIMP, lib, "class_getMethodImplementation")
	})
	return objcRuntime.err
}

// realClass is obj's own class, under the subclass key-value observing
// slips in: every Wails window and web view is observed, so swapping an
// instance's class would undo the observing.
func realClass(obj objc.ID) objc.Class { return objc.Class(obj.Send(sel("class"))) }

// warner is a logger's warning.
type warner interface{ Warn(msg string, args ...any) }

// unready logs why the float could not be readied.
type unready struct{ log warner }

func (u unready) Warn(reason string, args ...any) {
	u.log.Warn("window: the float may take the keyboard in any mode and miss the mouse while not key", append([]any{"reason", reason}, args...)...)
}

// answerFor makes s, a method taking at most one object and answering a
// BOOL, answer answer() for the instance only, and what it answered before
// for every other instance of cls; types is its type encoding. False when cls
// has no such method to answer for.
func answerFor(only objc.ID, cls objc.Class, s objc.SEL, types string, answer func() bool) bool {
	if cls == 0 {
		return false
	}
	before := objcRuntime.methodIMP(cls, s)
	if before == 0 {
		return false
	}
	var prev func(self objc.ID, cmd objc.SEL, arg objc.ID) bool
	purego.RegisterFunc(&prev, uintptr(before))
	objcRuntime.replaceMethod(cls, s, objc.NewIMP(func(self objc.ID, cmd objc.SEL, arg objc.ID) bool {
		if self == only {
			return answer()
		}
		return prev(self, cmd, arg)
	}), types)
	return true
}

//go:embed float_pointer.js
var floatPointerScript string

// The pointer's moves floatPointerScript dispatches.
const (
	pointerEntered = "entered"
	pointerMoved   = "moved"
	pointerExited  = "exited"
)

// floatPointerJS calls floatPointerScript for kind at x, y in the page's
// coordinates.
func floatPointerJS(kind string, x, y float64) string {
	bridge, _ := json.Marshal(window.FloatBridge)
	k, _ := json.Marshal(kind)
	return floatPointerScript + "(" + string(bridge) + "," + string(k) + "," + strconv.FormatFloat(x, 'f', -1, 64) + "," + strconv.FormatFloat(y, 'f', -1, 64) + ")"
}

// floatTracked is what a tracking area's owner reaches: the float and its
// web view.
type floatTracked struct{ win, webview objc.ID }

// trackTargets maps each tracking area's owner to what it tracks.
var trackTargets sync.Map

// pointerTo dispatches the pointer's kind of move in the float's page while
// the float is not key: WebKit drops the mouse's moves in a window that is
// not key, so its page would see no hover, and a key float gets them from
// WebKit itself. It runs on the main thread.
func pointerTo(s objc.SEL, kind string) objc.MethodDef {
	return objc.MethodDef{Cmd: s, Fn: func(self objc.ID, _ objc.SEL, event objc.ID) {
		v, ok := trackTargets.Load(self)
		if !ok {
			return
		}
		t := v.(floatTracked)
		if objc.Send[bool](t.win, sel("isKeyWindow")) {
			return
		}
		at := objc.Send[point](t.webview, sel("convertPoint:fromView:"), objc.Send[point](event, sel("locationInWindow")), objc.ID(0))
		t.webview.Send(sel("evaluateJavaScript:completionHandler:"), nsString(floatPointerJS(kind, at.X, at.Y)), objc.ID(0))
	}}
}

// floatOnly gives the float, and no other window, what a panel that is not
// key needs: its panel becomes key only while keyable says so, its web view
// takes the first click, and a tracking area active in any window hands its
// page the pointer (pointerTo). The first two replace a method on the class
// the float shares with the omnibox panel and every web view, answering as
// before for those. Whatever it cannot do it logs to log; it runs on the main
// thread, once.
func floatOnly(win objc.ID, keyable func() bool, log warner) {
	u := unready{log}
	if win == 0 {
		u.Warn("no float window")
		return
	}
	content := win.Send(sel("contentView"))
	webview := findWebView(content)
	if webview == 0 {
		u.Warn("the float has no web view")
		return
	}
	if err := loadObjcRuntime(); err != nil {
		u.Warn("the Objective-C runtime did not load", "error", err)
		return
	}
	if !answerFor(win, realClass(win), sel("canBecomeKeyWindow"), "B@:", keyable) {
		u.Warn("the float's panel has no class to answer for")
	}
	if !answerFor(webview, realClass(webview), sel("acceptsFirstMouse:"), "B@:@", func() bool { return true }) {
		u.Warn("the float's web view has no class to answer for")
	}
	tracker, err := objc.RegisterClass(brand.Name+"FloatTracker", objc.GetClass("NSObject"), nil, nil, []objc.MethodDef{
		pointerTo(sel("mouseEntered:"), pointerEntered), pointerTo(sel("mouseMoved:"), pointerMoved), pointerTo(sel("mouseExited:"), pointerExited),
	})
	if err != nil {
		u.Warn("the float's tracking did not register", "error", err)
		return
	}
	owner := objc.ID(tracker).Send(sel("new"))
	trackTargets.Store(owner, floatTracked{win: win, webview: webview})
	area := class("NSTrackingArea").Send(sel("alloc")).Send(sel("initWithRect:options:owner:userInfo:"), rect{}, uint(floatTracking), owner, objc.ID(0))
	content.Send(sel("addTrackingArea:"), area)
}

// userDefaults is the app's standard user defaults, whose domain is its
// bundle identifier. It runs on any thread, inside its own autorelease pool,
// since the threads Go runs it on have none.
type userDefaults struct{}

func withPool(fn func()) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	pool := class("NSAutoreleasePool").Send(sel("new"))
	defer pool.Send(sel("drain"))
	fn()
}

func standardDefaults() objc.ID { return class("NSUserDefaults").Send(sel("standardUserDefaults")) }

func (userDefaults) String(key string) (value string) {
	withPool(func() {
		if s := standardDefaults().Send(sel("stringForKey:"), nsString(key)); s != 0 {
			value = objc.Send[string](s, sel("UTF8String"))
		}
	})
	return value
}

func (userDefaults) SetString(key, value string) {
	withPool(func() { standardDefaults().Send(sel("setObject:forKey:"), nsString(value), nsString(key)) })
}
