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
