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

// words reads a client string by key, as *texts.Catalog does.
type words interface {
	T(key string, params map[string]any) string
}

// menuOf is the application menu, worded by w: the Edit menu an accessory
// app lacks, the Window menu that gives ⌘W and ⌘M, and Quit.
func menuOf(name string, w words) []menuItem {
	named := map[string]any{"name": name}
	return []menuItem{
		{title: name, items: []menuItem{
			{title: w.T("Hide {name}", named), role: roleHide},
			{title: w.T("Hide Others", nil), role: roleHideOthers},
			{title: w.T("Show All", nil), role: roleShowAll},
			separator,
			{title: w.T("Quit {name}", named), role: roleQuit},
		}},
		{title: w.T("Edit", nil), items: []menuItem{
			{title: w.T("Undo", nil), role: roleUndo},
			{title: w.T("Redo", nil), role: roleRedo},
			separator,
			{title: w.T("Cut", nil), role: roleCut},
			{title: w.T("Copy", nil), role: roleCopy},
			{title: w.T("Paste", nil), role: rolePaste},
			{title: w.T("Select All", nil), role: roleSelectAll},
		}},
		{title: w.T("Window", nil), items: []menuItem{
			{title: w.T("Minimize", nil), role: roleMinimise},
			{title: w.T("Zoom", nil), role: roleZoom},
			separator,
			{title: w.T("Close", nil), role: roleClose},
		}},
	}
}
