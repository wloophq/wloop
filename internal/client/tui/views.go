package tui

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/termfmt"
)

// The GTD views a list groups.
const (
	viewFocus   = "focus"
	viewInbox   = "inbox"
	viewOutbox  = "outbox"
	viewNext    = "next"
	viewWaiting = "waiting"
)

// taskProvider names an entry in the busy map, which is keyed by unit.
const taskProvider unit.ProviderID = "entry"

func taskUnit(id string) unit.Unit { return unit.Unit{Provider: taskProvider, Key: id} }

// SetViews takes the core views of GET /api/gtd. The first time, before the
// user moved to a view, it shows Focus.
func (m *Model) SetViews(c api.ViewsCatalog) {
	current := m.viewKey()
	if !m.navigated {
		current = "gtd:" + viewFocus
	}
	m.viewsCat, m.taskViews = c, nil
	for _, v := range c.Views {
		m.taskViews = append(m.taskViews, View{ID: v.ID, Label: v.Label})
	}
	m.goTo(current)
}

// SetTasks replaces the entries of view, as its last page left them, and
// the counters that page carried.
func (m *Model) SetTasks(view string, tasks []api.TaskView, c api.Counters) {
	if m.tasks == nil {
		m.tasks = map[string][]api.TaskView{}
	}
	m.tasks[view] = tasks
	m.tasksLoaded = true
	m.SetCounters(c)
	if d := m.opened; d != nil {
		if i := slices.IndexFunc(tasks, func(e api.TaskView) bool { return e.ID == d.ID }); i >= 0 && tasks[i].Updated.After(d.Updated) {
			m.detailStale = true
		}
	}
	m.keepTaskSelection()
}

// SetCounters replaces the counts the tabs show.
func (m *Model) SetCounters(c api.Counters) {
	if c.Area == m.area {
		m.counters = c
	}
}

// SetFilteredCounters takes the counts of area narrowed to the entries
// the filter keeps.
func (m *Model) SetFilteredCounters(area string, c api.Counters) {
	if area == m.area {
		m.filtered = &c
	}
}

// FilterTerms are the filter's words.
func (m *Model) FilterTerms() []string { return strings.Fields(string(m.filter)) }

// tabCounters are the counts the GTD tabs show: with a filter, those of the
// entries it keeps, the last read while a newer one is on its way; nil
// before the first.
func (m *Model) tabCounters() *api.Counters {
	if len(m.FilterTerms()) == 0 {
		return &m.counters
	}
	return m.filtered
}

// viewsBase is the index of the first GTD view, after the plan.
func (m *Model) viewsBase() int { return m.planView() + 1 }

// viewOnScreen is the GTD view on screen.
func (m *Model) viewOnScreen() (View, bool) {
	if i := m.view - m.viewsBase(); i >= 0 && i < len(m.taskViews) {
		return m.taskViews[i], true
	}
	return View{}, false
}

// TaskQuery is the GTD view on screen and the area the list follows; ok
// is false outside the GTD views.
func (m *Model) TaskQuery() (view, area string, ok bool) {
	v, ok := m.viewOnScreen()
	if ok && v.ID == viewReview {
		st, shown := m.reviewStep()
		return st.View, "", shown
	}
	return v.ID, m.area, ok
}

// groupOf is the views Tab cycles through from view i: the GTD views, or
// the unit views (the catalog's rows views, the plan and the plugin views).
func (m *Model) groupOf(i int) []int {
	var out []int
	inViews := i >= m.viewsBase() && i < m.pluginBase()
	for j := range m.viewCount() {
		if (j >= m.viewsBase() && j < m.pluginBase()) == inViews {
			out = append(out, j)
		}
	}
	return out
}

// cycle shows the next view of the group on screen.
func (m *Model) cycle() {
	g := m.groupOf(m.view)
	i := slices.Index(g, m.view)
	m.setView(g[(i+1)%len(g)])
}

// visibleTasks are the entries of the GTD view on screen the filter
// keeps, in the order the list shows them: grouped by context in Next and
// by person in Waiting.
func (m *Model) visibleTasks() []api.TaskView {
	v, ok := m.viewOnScreen()
	if !ok {
		return nil
	}
	q, view := string(m.filter), m.listView(v)
	var out []api.TaskView
	for _, e := range m.tasks[view] {
		if taskMatches(e, q) {
			out = append(out, e)
		}
	}
	if key := m.grouping(view); key != nil {
		order := m.groupOrder(out, key)
		slices.SortStableFunc(out, func(a, b api.TaskView) int {
			return cmp.Compare(slices.Index(order, key(a)), slices.Index(order, key(b)))
		})
	}
	return out
}

// grouping is the key a view groups its entries by, nil when it does not.
func (m *Model) grouping(view string) func(api.TaskView) string {
	switch view {
	case viewNext:
		return func(e api.TaskView) string {
			if len(e.Contexts) == 0 {
				return ""
			}
			return e.Contexts[0]
		}
	case viewWaiting:
		return func(e api.TaskView) string {
			if e.Waiting == nil || len(e.Waiting.On) == 0 {
				return ""
			}
			return e.Waiting.On[0].Name
		}
	}
	return nil
}

// groupOrder is the groups of entries in the order they show: the served
// contexts' order, then the others by name, the empty group last.
func (m *Model) groupOrder(tasks []api.TaskView, key func(api.TaskView) string) []string {
	seen := map[string]bool{}
	var known, other []string
	empty := false
	for _, e := range tasks {
		k := key(e)
		switch {
		case seen[k]:
		case k == "":
			empty = true
		case slices.Contains(m.viewsCat.Contexts, k):
			known = append(known, k)
		default:
			other = append(other, k)
		}
		seen[k] = true
	}
	slices.SortFunc(known, func(a, b string) int {
		return cmp.Compare(slices.Index(m.viewsCat.Contexts, a), slices.Index(m.viewsCat.Contexts, b))
	})
	slices.Sort(other)
	out := append(known, other...)
	if empty {
		out = append(out, "")
	}
	return out
}

// groupHead names a group of view.
func groupHead(view, key string) string {
	switch {
	case view == viewNext && key == "":
		return "no context"
	case view == viewNext:
		return "@" + key
	case key == "":
		return "waiting on nobody named"
	}
	return "waiting on " + key
}

// taskMatches reports whether every word of query is in one of the
// entry's texts.
func taskMatches(e api.TaskView, query string) bool {
	return api.TaskMatches(e, strings.Fields(query))
}

func (m *Model) taskCursor(tasks []api.TaskView) int {
	if i := slices.IndexFunc(tasks, func(e api.TaskView) bool { return e.ID == m.taskSel }); i >= 0 {
		return i
	}
	return min(max(m.taskAt, 0), max(len(tasks)-1, 0))
}

func (m *Model) keepTaskSelection() {
	if _, ok := m.viewOnScreen(); !ok {
		return
	}
	tasks := m.visibleTasks()
	if len(tasks) == 0 {
		m.taskSel = ""
		return
	}
	m.taskAt = m.taskCursor(tasks)
	m.taskSel = tasks[m.taskAt].ID
}

func (m *Model) selectedTask() (api.TaskView, bool) {
	tasks := m.visibleTasks()
	if len(tasks) == 0 || (m.rv.head && m.inReview()) {
		return api.TaskView{}, false
	}
	return tasks[m.taskCursor(tasks)], true
}

func (m *Model) taskMove(delta int) {
	tasks := m.visibleTasks()
	if len(tasks) == 0 {
		return
	}
	m.taskAt = min(max(m.taskCursor(tasks)+delta, 0), len(tasks)-1)
	m.taskSel = tasks[m.taskAt].ID
	m.closeDetail()
}

// taskLabel names an entry on the status line.
func taskLabel(e api.TaskView) string {
	return termfmt.Cut(oneLine(cmp.Or(e.Title, e.ID)), 48)
}

// rowOf is the row of u the model holds.
func (m *Model) rowOf(u unit.Unit) (api.Row, bool) {
	i := slices.IndexFunc(m.rows, func(r api.Row) bool { return r.Unit == u })
	if i < 0 {
		return api.Row{}, false
	}
	return m.rows[i], true
}

// sourceLink is the link of the entry's first unit: its row's, else its
// card's in the open detail.
func (m *Model) sourceLink(e api.TaskView) string {
	if len(e.Units) == 0 {
		return ""
	}
	u := e.Units[0]
	if r, ok := m.rowOf(u); ok && r.Link != "" {
		return r.Link
	}
	for _, c := range e.Cards {
		if c.Card.Unit == u {
			return c.Card.Link
		}
	}
	if d := m.opened; d != nil && d.ID == e.ID {
		for _, c := range d.Cards {
			if c.Card.Unit == u {
				return c.Card.Link
			}
		}
	}
	return ""
}

// renderTasks draws the GTD view on screen: its entries, the open
// detail below them.
func (m *Model) renderTasks(lines []string, v View, width, body int, now time.Time) []string {
	tasks := m.visibleTasks()
	if len(tasks) == 0 {
		text := "Nothing in " + v.Label + " right now."
		switch {
		case !m.tasksLoaded:
			text = "loading…"
		case len(m.filter) > 0:
			text = "Nothing in " + v.Label + " matches \"" + string(m.filter) + "\"."
		}
		return append(lines, "  "+m.t.Dim(termfmt.Cut(text, width-2)))
	}
	cur := m.taskCursor(tasks)
	if m.rv.head && m.inReview() {
		cur = -1
	}
	var detail []string
	if cur >= 0 && m.detailOpen(tasks[cur]) && width >= 40 && body >= 8 {
		detail = m.taskDetail(m.shownDetail(tasks[cur]), width, now)
		room := body - min(len(tasks), max(body/3, 3)) - 1
		if len(detail) > room && room >= 3 {
			detail = append(detail[:room-1], "  "+m.t.Dim(termfmt.Ellipsis))
		}
		if room < 3 {
			detail = nil
		}
	}
	listHeight := body
	if len(detail) > 0 {
		listHeight -= len(detail) + 1
	}
	lines = append(lines, m.taskList(v.ID, tasks, cur, width, listHeight, now)...)
	if len(detail) > 0 {
		lines = append(append(lines, ""), detail...)
	}
	return lines
}

// taskList lays the entries out in columns, with a head line before each
// group, and shows the height of them around the cursor.
func (m *Model) taskList(view string, tasks []api.TaskView, cur, width, height int, now time.Time) []string {
	t := m.t
	if view == viewOutbox {
		return m.outboxList(tasks, cur, width, height, now)
	}
	cols := []termfmt.Column{{Right: true}, {Flex: true}}
	if width >= 60 {
		cols = append(cols, termfmt.Column{Max: min(max((width-2)/3, 14), 40)})
	}
	cells := make([][]termfmt.Cell, len(tasks))
	for i, e := range tasks {
		age, style := m.taskAge(e, now)
		title := oneLine(e.Title)
		if e.Focus {
			title = "★ " + title
		}
		c := []termfmt.Cell{{Text: age, Style: style}, {Text: title}}
		if len(cols) > 2 {
			c = append(c, termfmt.Cell{Text: m.taskWhere(e), Style: t.Dim})
		}
		cells[i] = c
	}
	laid := t.Columns(width-3, 2, cols, cells)
	key := m.grouping(view)
	var out []string
	var at []int
	for i, e := range tasks {
		if key != nil && (i == 0 || key(tasks[i-1]) != key(e)) {
			out, at = append(out, " "+t.Bold(termfmt.Cut(groupHead(view, key(e)), width-1))), append(at, -1)
		}
		out, at = append(out, m.taskLine(e, laid[i], i == cur, width)), append(at, i)
	}
	return window(out, slices.Index(at, cur), height)
}

// taskLine marks the cursor, a busy entry and a changed one.
func (m *Model) taskLine(e api.TaskView, laid string, selected bool, width int) string {
	t := m.t
	mark := []rune("   ")
	if selected {
		mark[0] = '>'
	}
	if _, busy := m.busy[taskUnit(e.ID)]; busy {
		mark[1] = '*'
	} else if e.Changed {
		mark[1] = '•'
	}
	line := string(mark) + laid
	if selected && t.Color {
		line = t.Reverse(line + strings.Repeat(" ", max(width-termfmt.Cells(termfmt.Plain(line)), 0)))
	}
	return line
}

// window is the height of lines that keeps line cur in sight.
func window(lines []string, cur, height int) []string {
	if height <= 0 {
		return nil
	}
	start := max(0, cur-height+1)
	return lines[start:min(len(lines), start+height)]
}

// taskAge is how long the entry waits: since its waiting started, else
// since it last changed.
func (m *Model) taskAge(e api.TaskView, now time.Time) (string, func(string) string) {
	since := e.Updated
	if e.Waiting != nil && !e.Waiting.Since.IsZero() {
		since = e.Waiting.Since
	}
	if since.IsZero() || !now.After(since) {
		return "-", m.t.Dim
	}
	if now.Sub(since) >= 48*time.Hour {
		return span(now.Sub(since)), m.t.Yellow
	}
	return span(now.Sub(since)), m.t.Dim
}

// taskWhere is the entry's contexts and its first unit, or its project.
func (m *Model) taskWhere(e api.TaskView) string {
	var parts []string
	for _, c := range e.Contexts {
		parts = append(parts, "@"+c)
	}
	parts = append(parts, projectFacts(e)...)
	switch {
	case len(e.Units) > 0:
		parts = append(parts, e.UnitLabel(0))
	case e.ProjectTitle != "":
		parts = append(parts, e.ProjectTitle)
	}
	return termfmt.Clean(strings.Join(parts, " "))
}

// viewTabs are the GTD views with their counts.
func (m *Model) viewTabs() []string {
	out := make([]string, len(m.taskViews))
	views := map[string]int{}
	if c := m.tabCounters(); c != nil {
		views = c.Views
	}
	for i, v := range m.taskViews {
		if n, ok := views[v.ID]; ok {
			out[i] = fmt.Sprintf("%s %d", v.Label, n)
			continue
		}
		out[i] = v.Label
	}
	return out
}

// taskSet is one view's entries as the pages left them, in the daemon's
// order, and the position to pull the next page from.
type taskSet struct {
	since api.Since
	order []string
	byID  map[string]api.TaskView
}

// apply takes a page: false when it is a delta the set cannot take, from
// another epoch or before any full page. An older delta is ignored.
func (s *taskSet) apply(p api.TasksPage) bool {
	switch {
	case p.Full:
		s.order, s.byID = nil, map[string]api.TaskView{}
	case s.since.Epoch == "" || p.Epoch != s.since.Epoch:
		return false
	case p.Rev < s.since.Rev:
		return true
	}
	for _, e := range p.Tasks {
		if _, ok := s.byID[e.ID]; !ok {
			s.order = append(s.order, e.ID)
		}
		s.byID[e.ID] = e
	}
	for _, id := range p.Removed {
		if _, ok := s.byID[id]; ok {
			delete(s.byID, id)
			s.order = slices.DeleteFunc(s.order, func(x string) bool { return x == id })
		}
	}
	s.since = api.Since{Epoch: p.Epoch, Rev: p.Rev}
	return true
}

func (s *taskSet) list() []api.TaskView {
	out := make([]api.TaskView, len(s.order))
	for i, id := range s.order {
		out[i] = s.byID[id]
	}
	return out
}
