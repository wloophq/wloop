// Package wails hosts the cockpit in a Wails window and panel on macOS; it is
// the only package that imports Wails.
package wails

type role int

const (
	roleSeparator role = iota + 1
	roleHide
	roleHideOthers
	roleShowAll
	roleQuit
	roleUndo
	roleRedo
	roleCut
	roleCopy
	rolePaste
	roleSelectAll
	roleMinimise
	roleZoom
	roleClose
)

type menuItem struct {
	title string
	role  role
	items []menuItem
}

var separator = menuItem{role: roleSeparator}

// menuOf is the application menu, worded by t: the Edit menu an accessory
// app lacks, the Window menu that gives ⌘W and ⌘M, and Quit.
func menuOf(name string, t func(key string, params map[string]any) string) []menuItem {
	named := map[string]any{"name": name}
	item := func(key string, r role) menuItem { return menuItem{title: t(key, nil), role: r} }
	return []menuItem{
		{title: name, items: []menuItem{
			{title: t("Hide {name}", named), role: roleHide},
			item("Hide Others", roleHideOthers),
			item("Show All", roleShowAll),
			separator,
			{title: t("Quit {name}", named), role: roleQuit},
		}},
		{title: t("Edit", nil), items: []menuItem{
			item("Undo", roleUndo),
			item("Redo", roleRedo),
			separator,
			item("Cut", roleCut),
			item("Copy", roleCopy),
			item("Paste", rolePaste),
			item("Select All", roleSelectAll),
		}},
		{title: t("Window", nil), items: []menuItem{
			item("Minimize", roleMinimise),
			item("Zoom", roleZoom),
			separator,
			item("Close", roleClose),
		}},
	}
}
