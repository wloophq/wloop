//go:build darwin

// Package macos implements the app layer's tray and notifier on AppKit and
// UserNotifications through purego, without cgo.
package macos

import (
	"runtime"

	"github.com/ebitengine/purego/objc"

	"github.com/wloophq/wloop/internal/client/menubar"
)

const activationPolicyAccessory = 1

func Available() bool { return true }

// Run starts AppKit and calls setup once it has launched; notification
// images are written under imageDir. AppKit only works
// on the process's main thread, so Run must be called from the main
// goroutine, which the main package pins to that thread in init. It never
// returns: terminating the app ends the process.
func Run(imageDir string, setup func(tray menubar.Tray, notes menubar.Notifier)) {
	runtime.LockOSThread()
	loadFrameworks()
	setupPump()
	nsApp := class("NSApplication").Send(sel("sharedApplication"))
	nsApp.Send(sel("setActivationPolicy:"), activationPolicyAccessory)
	launched = func() { setup(newTray(), newNotifier(imageDir)) }
	nsApp.Send(sel("setDelegate:"), pump)
	nsApp.Send(sel("run"))
}

// Quit ends the process through AppKit; before Run it does nothing.
func Quit() {
	if pump == 0 {
		return
	}
	onMain(func() { class("NSApplication").Send(sel("sharedApplication")).Send(sel("terminate:"), objc.ID(0)) })
}
