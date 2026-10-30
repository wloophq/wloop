package tui

import (
	"slices"

	"github.com/wloophq/wloop/internal/api"
)

// SetPlugins keeps the plugins the daemon serves, whose views the catalog
// lists, keeping the current view by id.
func (m *Model) SetPlugins(ps []api.Plugin) {
	current := m.viewKey()
	m.served = slices.Clone(ps)
	m.goTo(current)
}

// viewCount is how many views there are: the row views, the plan and the
// task views.
func (m *Model) viewCount() int { return m.viewsBase() + len(m.taskViews) }

// viewKey names the current view across a reload of the views.
func (m *Model) viewKey() string {
	if v, ok := m.viewOnScreen(); ok {
		return "view:" + v.ID
	}
	switch {
	case m.view < len(m.views):
		return "rows:" + m.views[m.view].ID
	case len(m.views) > 0 && m.view == m.planView():
		return "plan"
	}
	return ""
}

// goTo shows the view key names, else Focus, else the first view.
func (m *Model) goTo(key string) {
	m.view = 0
	if len(m.taskViews) > 0 {
		m.view = m.viewsBase()
	}
	switch {
	case key == "plan":
		m.view = m.planView()
	case slices.ContainsFunc(m.views, func(v View) bool { return "rows:"+v.ID == key }):
		m.view = slices.IndexFunc(m.views, func(v View) bool { return "rows:"+v.ID == key })
	case slices.ContainsFunc(m.taskViews, func(v View) bool { return "view:"+v.ID == key }):
		m.view = m.viewsBase() + slices.IndexFunc(m.taskViews, func(v View) bool { return "view:"+v.ID == key })
	}
	m.keepSelection()
	m.keepTaskSelection()
}
