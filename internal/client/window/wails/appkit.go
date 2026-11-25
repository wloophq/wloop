//go:build darwin && purego

package wails

import (
	"github.com/ebitengine/purego/objc"
	"github.com/wailsapp/wails/v3/pkg/application"

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

// screens is every screen's visible frame, without the menu bar and the
// Dock, the main screen first; it runs on the main thread.
func screens() []window.Rect {
	all := class("NSScreen").Send(sel("screens"))
	n := objc.Send[int](all, sel("count"))
	if n == 0 {
		return nil
	}
	mainHeight := objc.Send[rect](all.Send(sel("objectAtIndex:"), 0), sel("frame")).Size.Height
	out := make([]window.Rect, 0, n)
	for i := range n {
		out = append(out, topLeft(objc.Send[rect](all.Send(sel("objectAtIndex:"), i), sel("visibleFrame")), mainHeight))
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
// active app's window. It runs on the main thread.
func handBackKeyboard(win objc.ID) {
	win.Send(sel("orderOut:"), objc.ID(0))
	win.Send(sel("orderFrontRegardless"))
}

// userDefaults is the app's standard user defaults, whose domain is its
// bundle identifier.
type userDefaults struct{}

func standardDefaults() objc.ID { return class("NSUserDefaults").Send(sel("standardUserDefaults")) }

func (userDefaults) String(key string) string {
	s := standardDefaults().Send(sel("stringForKey:"), nsString(key))
	if s == 0 {
		return ""
	}
	return objc.Send[string](s, sel("UTF8String"))
}

func (userDefaults) SetString(key, value string) {
	standardDefaults().Send(sel("setObject:forKey:"), nsString(value), nsString(key))
}
