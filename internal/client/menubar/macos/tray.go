//go:build darwin

package macos

import (
	"github.com/ebitengine/purego/objc"

	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/client/menubar"
)

const (
	variableStatusItemLength = -1.0
	controlStateOn           = 1

	statusItemBehaviorRemovable = 1 << 1
)

var statusItemAutosaveName = brand.LaunchAgent() + ".status"

type tray struct {
	item objc.ID
}

func newTray() *tray {
	item := class("NSStatusBar").Send(sel("systemStatusBar")).Send(sel("statusItemWithLength:"), float64(variableStatusItemLength))
	// Without an autosave name Control Center forgets the position and restarts at the leftmost slot, under the notch.
	item.Send(sel("setAutosaveName:"), nsString(statusItemAutosaveName))
	item.Send(sel("setBehavior:"), statusItemBehaviorRemovable)
	// NSStatusBar does not retain the item.
	item.Send(sel("retain"))
	item.Send(sel("button")).Send(sel("setTitle:"), nsString("PR"))
	return &tray{item: item}
}

func (t *tray) SetTitle(title string) {
	onMain(func() { t.item.Send(sel("button")).Send(sel("setTitle:"), nsString(title)) })
}

func (t *tray) SetMenu(items []menubar.MenuItem) {
	onMain(func() {
		resetActions()
		menu := buildMenu(items)
		t.item.Send(sel("setMenu:"), menu)
		menu.Send(sel("release"))
	})
}

// buildMenu returns an owned menu; the caller releases it.
func buildMenu(items []menubar.MenuItem) objc.ID {
	menu := class("NSMenu").Send(sel("alloc")).Send(sel("initWithTitle:"), nsString(""))
	menu.Send(sel("setAutoenablesItems:"), false)
	for _, it := range items {
		if it.Separator {
			menu.Send(sel("addItem:"), class("NSMenuItem").Send(sel("separatorItem")))
			continue
		}
		mi := class("NSMenuItem").Send(sel("alloc")).Send(sel("initWithTitle:action:keyEquivalent:"),
			nsString(it.Title), sel("clicked:"), nsString(""))
		if it.Action != nil {
			mi.Send(sel("setTag:"), tagFor(it.Action))
			mi.Send(sel("setTarget:"), pump)
		} else {
			mi.Send(sel("setAction:"), objc.SEL(0))
		}
		if it.Tooltip != "" {
			mi.Send(sel("setToolTip:"), nsString(it.Tooltip))
		}
		mi.Send(sel("setEnabled:"), it.Enabled && (it.Action != nil || len(it.Children) > 0))
		if it.Checked {
			mi.Send(sel("setState:"), controlStateOn)
		}
		if len(it.Children) > 0 {
			sub := buildMenu(it.Children)
			mi.Send(sel("setSubmenu:"), sub)
			sub.Send(sel("release"))
		}
		menu.Send(sel("addItem:"), mi)
		mi.Send(sel("release"))
	}
	return menu
}
