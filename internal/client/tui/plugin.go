package tui

import (
	"slices"
	"sync"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/client/rowset"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/termfmt"
)

// PluginView is a plugin's own TUI view.
type PluginView interface {
	ID() string
	Label() string
	Render(set *rowset.Set, width, height int) []string
	Key(k string, set *rowset.Set) (Command, bool)
}

type registration struct {
	provider unit.ProviderID
	view     PluginView
}

var (
	registryMu sync.Mutex
	registered []registration
)

// RegisterView is called from the plugin's own tui package, wired in
// internal/cli. A view of the same provider and id replaces the earlier one.
func RegisterView(provider unit.ProviderID, v PluginView) {
	registryMu.Lock()
	defer registryMu.Unlock()
	if i := slices.IndexFunc(registered, func(r registration) bool { return r.provider == provider && r.view.ID() == v.ID() }); i >= 0 {
		registered[i].view = v
		return
	}
	registered = append(registered, registration{provider, v})
}

// SetPlugins shows the registered views of the enabled plugins that list
// them in their UI, after the plan, keeping the current view by id.
func (m *Model) SetPlugins(ps []api.Plugin) {
	current := m.viewKey()
	registryMu.Lock()
	regs := slices.Clone(registered)
	registryMu.Unlock()
	m.plugins, m.served = nil, slices.Clone(ps)
	for _, r := range regs {
		if slices.ContainsFunc(ps, func(p api.Plugin) bool {
			return p.ID == r.provider && p.Enabled && p.UI != nil && slices.Contains(p.UI.TUIViews, r.view.ID())
		}) {
			m.plugins = append(m.plugins, r.view)
		}
	}
	m.goTo(current)
}

// pluginBase is the index of the first plugin view: the plugin views come
// after every core view.
func (m *Model) pluginBase() int { return m.viewsBase() + len(m.taskViews) }

// plugin is the plugin view on screen.
func (m *Model) plugin() (PluginView, bool) {
	if i := m.view - m.pluginBase(); i >= 0 && i < len(m.plugins) {
		return m.plugins[i], true
	}
	return nil, false
}

// viewCount is how many views there are: the row views, the plan, the task
// views and the plugin views.
func (m *Model) viewCount() int { return m.pluginBase() + len(m.plugins) }

// viewKey names the current view across a reload of the views.
func (m *Model) viewKey() string {
	if v, ok := m.plugin(); ok {
		return "plugin:" + v.ID()
	}
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
	case slices.ContainsFunc(m.plugins, func(v PluginView) bool { return "plugin:"+v.ID() == key }):
		m.view = m.pluginBase() + slices.IndexFunc(m.plugins, func(v PluginView) bool { return "plugin:"+v.ID() == key })
	case slices.ContainsFunc(m.taskViews, func(v View) bool { return "view:"+v.ID == key }):
		m.view = m.viewsBase() + slices.IndexFunc(m.taskViews, func(v View) bool { return "view:"+v.ID == key })
	}
	m.keepSelection()
	m.keepTaskSelection()
}

// rowSet is the rows as a plugin view reads them, built once per SetData.
func (m *Model) rowSet() *rowset.Set {
	if m.set == nil {
		m.set = &rowset.Set{}
		m.set.Apply(api.RowsPage{Full: true, Rows: m.rows})
	}
	return m.set
}

// pluginKey gives the key to the plugin view, by its rune or its name.
func (m *Model) pluginKey(v PluginView, k Key) (Command, bool) {
	name := k.Name
	if k.Rune != 0 {
		name = string(k.Rune)
	}
	c, ok := v.Key(name, m.rowSet())
	if !ok {
		return Command{}, false
	}
	return m.send(c)
}

// renderPlugin draws the tabs, the plugin view's lines cut to the screen,
// the status line and the help.
func (m *Model) renderPlugin(v PluginView, width, height int) []string {
	lines := []string{m.tabs(width)}
	if height >= 7 {
		lines = append(lines, "")
	}
	body := height - len(lines) - 2
	for _, l := range v.Render(m.rowSet(), width, body) {
		if len(lines) == height-2 {
			break
		}
		lines = append(lines, termfmt.Cut(l, width))
	}
	return append(pad(lines, height-2), m.statusLine(width), m.bottom(width))
}
