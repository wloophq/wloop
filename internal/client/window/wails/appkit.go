//go:build darwin && purego

package wails

import (
	"github.com/ebitengine/purego/objc"
	"github.com/wailsapp/wails/v3/pkg/application"
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
// the keyboard focus, whichever app owns it; it runs on the main thread.
func centerOnFocusedScreen(w *application.WebviewWindow) {
	screen := class("NSScreen").Send(sel("mainScreen"))
	if screen == 0 {
		return
	}
	win := nsWindow(w)
	area := objc.Send[rect](screen, sel("visibleFrame"))
	frame := objc.Send[rect](win, sel("frame"))
	win.Send(sel("setFrameOrigin:"), centeredIn(area, frame.Size))
}
