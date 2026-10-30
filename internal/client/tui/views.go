package tui

import (
	"cmp"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/wloophq/wloop/internal/api"
	dtask "github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/termfmt"
)

// taskProvider names a task in the busy map, which is keyed by unit.
const taskProvider = unit.Task

func taskUnit(id string) unit.Unit { return unit.Unit{Provider: taskProvider, Key: id} }

// SetViews takes the profile's views of GET /api/views. The first time, before
// the user moved to a view, it shows the first.
func (m *Model) SetViews(c api.ViewsCatalog) {
	current := m.viewKey()
	if !m.navigated && len(c.Views) > 0 {
		current = "view:" + c.Views[0].ID
	}
	m.viewsCat, m.taskViews = c, nil
	for _, v := range c.Views {
		m.taskViews = append(m.taskViews, View{ID: v.ID, Label: v.Label})
	}
	m.goTo(current)
}

// SetLoading takes the plugins the last page of tasks said are still loading.
func (m *Model) SetLoading(ls []api.Loading) { m.tasksLoading = ls }

// viewSpec is the served spec of task view id.
func (m *Model) viewSpec(id string) api.TaskViewSpec {
	i := slices.IndexFunc(m.viewsCat.Views, func(v api.TaskViewSpec) bool { return v.ID == id })
	if i < 0 {
		return api.TaskViewSpec{ID: id}
	}
	return m.viewsCat.Views[i]
}

// SetTasks replaces the tasks of view, as its last page left them, and
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

// SetFilteredCounters takes the counts of area narrowed to the tasks
// the filter keeps.
func (m *Model) SetFilteredCounters(area string, c api.Counters) {
	if area == m.area {
		m.filtered = &c
	}
}

// FilterTerms are the filter's words.
func (m *Model) FilterTerms() []string { return strings.Fields(string(m.filter)) }

// tabCounters are the counts the task tabs show: with a filter, those of the
// tasks it keeps, the last read while a newer one is on its way; nil
// before the first.
func (m *Model) tabCounters() *api.Counters {
	if len(m.FilterTerms()) == 0 {
		return &m.counters
	}
	return m.filtered
}

// viewsBase is the index of the first task view, after the plan.
func (m *Model) viewsBase() int { return m.planView() + 1 }

// viewOnScreen is the task view on screen.
func (m *Model) viewOnScreen() (View, bool) {
	if i := m.view - m.viewsBase(); i >= 0 && i < len(m.taskViews) {
		return m.taskViews[i], true
	}
	return View{}, false
}

// TaskQuery is the task view on screen and the area the list follows; ok
// is false outside the task views.
func (m *Model) TaskQuery() (view, area string, ok bool) {
	v, ok := m.viewOnScreen()
	if ok && v.ID == ctxReview {
		st, shown := m.reviewStep()
		return st.View, "", shown
	}
	return v.ID, m.area, ok
}

// groupOf is the views Tab cycles through from view i: the task views, or
// the unit views (the catalog's rows views and the plan).
func (m *Model) groupOf(i int) []int {
	var out []int
	inViews := i >= m.viewsBase()
	for j := range m.viewCount() {
		if (j >= m.viewsBase()) == inViews {
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

// visibleTasks are the tasks of the task view on screen the filter
// keeps, in the order the list shows them: grouped by the fact the view's
// spec groups by.
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
		order := m.groupOrder(view, out, key)
		slices.SortStableFunc(out, func(a, b api.TaskView) int {
			return cmp.Compare(slices.Index(order, key(a)), slices.Index(order, key(b)))
		})
	}
	return out
}

// grouping is the key a view groups its tasks by, the fact its spec names,
// nil when it names none.
func (m *Model) grouping(view string) func(api.TaskView) string {
	fact := m.viewSpec(view).Group
	if fact == "" {
		return nil
	}
	return func(e api.TaskView) string { return m.groupValue(fact, e) }
}

// groupValue is e's value of fact: a field of the task as the wire names
// it, or task.prop.<name> a property, the first of a list.
func (m *Model) groupValue(fact string, e api.TaskView) string {
	name := strings.TrimPrefix(fact, "task.")
	var raw json.RawMessage
	if prop, ok := strings.CutPrefix(name, "prop."); ok {
		raw = e.Props[prop]
	} else {
		b, _ := json.Marshal(e)
		var fields map[string]json.RawMessage
		_ = json.Unmarshal(b, &fields)
		raw = fields[name]
	}
	var v any
	if json.Unmarshal(raw, &v) != nil {
		return ""
	}
	if l, ok := v.([]any); ok {
		if len(l) == 0 {
			return ""
		}
		v = l[0]
	}
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case map[string]any:
		return fmt.Sprint(cmp.Or(x["name"], x["key"], ""))
	}
	return fmt.Sprint(v)
}

// groupOrder is the groups of tasks in the order they show: a state's or a
// category's in the served states' order, a context's in the served
// contexts' order, then the others by name, the empty group last.
func (m *Model) groupOrder(view string, tasks []api.TaskView, key func(api.TaskView) string) []string {
	order := m.groupRanks(view)
	seen := map[string]bool{}
	var known, other []string
	empty := false
	for _, e := range tasks {
		k := key(e)
		switch {
		case seen[k]:
		case k == "":
			empty = true
		case slices.Contains(order, k):
			known = append(known, k)
		default:
			other = append(other, k)
		}
		seen[k] = true
	}
	slices.SortFunc(known, func(a, b string) int { return cmp.Compare(slices.Index(order, a), slices.Index(order, b)) })
	slices.Sort(other)
	out := append(known, other...)
	if empty {
		out = append(out, "")
	}
	return out
}

// groupRanks are the values of view's group fact in the order the catalog
// serves them, none for a fact the catalog does not order.
func (m *Model) groupRanks(view string) []string {
	var out []string
	switch name := strings.TrimPrefix(m.viewSpec(view).Group, "task."); name {
	case "state", "category":
		for _, st := range m.viewsCat.States {
			v := st.Name
			if name == "category" {
				v = st.Category
			}
			if !slices.Contains(out, v) {
				out = append(out, v)
			}
		}
	default:
		if p, ok := m.viewsCat.PropOf(string(dtask.RoleContexts)); ok && name == "prop."+p.Name {
			out = m.viewsCat.Contexts
		}
	}
	return out
}

// groupHead names a group of view: a state by its label, a context with an
// @, any other value as it is.
func (m *Model) groupHead(view, key string) string {
	name := strings.TrimPrefix(m.viewSpec(view).Group, "task.")
	switch {
	case key == "":
		return "none"
	case name == "state":
		if i := slices.IndexFunc(m.viewsCat.States, func(st api.StateSpec) bool { return st.Name == key }); i >= 0 {
			return cmp.Or(m.viewsCat.States[i].Label, key)
		}
	case slices.Contains(m.groupRanks(view), key) && name != "category":
		return "@" + key
	}
	return key
}

// taskMatches reports whether every word of query is in one of the
// task's texts.
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

// taskLabel names a task on the status line.
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

// sourceLink is the link of the task's first unit: its row's, else its
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

// renderTasks draws the task view on screen: its tasks, the open
// detail below them.
func (m *Model) renderTasks(lines []string, v View, width, body int, now time.Time) []string {
	if l, ok := m.loadingLine(m.tasksLoading, width); ok {
		lines, body = append(lines, l), body-1
	}
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

// loadingLine says which plugins are still loading and why; ok is false when
// none is.
func (m *Model) loadingLine(ls []api.Loading, width int) (string, bool) {
	if len(ls) == 0 {
		return "", false
	}
	parts := make([]string, len(ls))
	for i, l := range ls {
		parts[i] = strings.TrimSuffix(l.Plugin+": "+l.Reason, ": ")
	}
	return "  " + m.t.Dim(termfmt.Cut(termfmt.Clean("loading "+strings.Join(parts, " · ")), width-2)), true
}

// taskList lays the tasks out in columns, with a head line before each
// group, and shows the height of them around the cursor.
func (m *Model) taskList(view string, tasks []api.TaskView, cur, width, height int, now time.Time) []string {
	t := m.t
	if m.viewSpec(view).Over == dtask.OverGates {
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
		if m.viewsCat.Starred(e) {
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
			out, at = append(out, " "+t.Bold(termfmt.Cut(m.groupHead(view, key(e)), width-1))), append(at, -1)
		}
		out, at = append(out, m.taskLine(e, laid[i], i == cur, width)), append(at, i)
	}
	return window(out, slices.Index(at, cur), height)
}

// taskLine marks the cursor, a busy task and a changed one.
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

// taskAge is how long the task has stood since it last changed.
func (m *Model) taskAge(e api.TaskView, now time.Time) (string, func(string) string) {
	since := e.Updated
	if since.IsZero() || !now.After(since) {
		return "-", m.t.Dim
	}
	if now.Sub(since) >= 48*time.Hour {
		return span(now.Sub(since)), m.t.Yellow
	}
	return span(now.Sub(since)), m.t.Dim
}

// contexts are e's contexts: the property the catalog's contexts role acts on.
func (m *Model) contexts(e api.TaskView) []string {
	p, ok := m.viewsCat.PropOf(string(dtask.RoleContexts))
	if !ok {
		return nil
	}
	return e.PropList(p.Name)
}

// taskWhere is the task's contexts and its first unit.
func (m *Model) taskWhere(e api.TaskView) string {
	var parts []string
	for _, c := range m.contexts(e) {
		parts = append(parts, "@"+c)
	}
	parts = append(parts, projectFacts(e)...)
	if len(e.Units) > 0 {
		parts = append(parts, e.UnitLabel(0))
	}
	return termfmt.Clean(strings.Join(parts, " "))
}

// viewTabs are the task views with their counts.
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

// taskSet is one view's tasks as the pages left them, in the daemon's
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
