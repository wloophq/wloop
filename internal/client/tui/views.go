package tui

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/api/lens"
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
	m.viewsCat, m.taskViews, m.rowWords = c, nil, nil
	m.refreshFacets()
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
	if m.taskItems == nil {
		m.taskItems = map[string][]lens.Item{}
	}
	m.taskItems[view] = make([]lens.Item, len(tasks))
	for i, e := range tasks {
		m.taskItems[view][i] = taskItem(e)
	}
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

// FilterQuery is the filter as typed, trimmed: the lens query the daemon counts with.
func (m *Model) FilterQuery() string { return strings.TrimSpace(string(m.filter)) }

// tabCounters are the counts the task tabs show: with a filter, those of the
// tasks it keeps, the last read while a newer one is on its way; nil
// before the first.
func (m *Model) tabCounters() *api.Counters {
	if m.FilterQuery() == "" {
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

// visibleTasks are the tasks of the task view on screen the filter keeps,
// in the order the list shows them.
func (m *Model) visibleTasks() []api.TaskView {
	tasks, _ := m.arrangedTasks()
	return tasks
}

// arrangedTasks are the visible tasks and, beside each, the head of the
// group it opens, "" for one inside a group.
func (m *Model) arrangedTasks() ([]api.TaskView, []string) {
	v, ok := m.viewOnScreen()
	if !ok {
		return nil, nil
	}
	lv, _ := m.lensViewOnScreen()
	order, heads := m.arrange(lv, lv.items)
	return reorder(m.tasks[m.listView(v)], order), heads
}

func (m *Model) taskCursor(tasks []api.TaskView) int {
	if m.taskAt >= 0 && m.taskAt < len(tasks) && tasks[m.taskAt].ID == m.taskSel {
		return m.taskAt
	}
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
	tasks, heads := m.arrangedTasks()
	if len(tasks) == 0 {
		text := m.viewSpec(v.ID).Empty
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
	lines = append(lines, m.taskList(v.ID, tasks, heads, cur, width, listHeight, now)...)
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

// taskList lays the tasks out as lines, with a head line before each group, and shows the height of them
// around the cursor.
func (m *Model) taskList(view string, tasks []api.TaskView, heads []string, cur, width, height int, now time.Time) []string {
	if m.viewSpec(view).Over == dtask.OverGates {
		return m.outboxList(tasks, cur, width, height, now)
	}
	lines := make([]listLine, len(tasks))
	for i, e := range tasks {
		lines[i] = m.taskLine(e, now)
	}
	return m.lineCells(lines, heads, cur, width, height)
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
	// moved are the units changes named since the set's last pull, whose tasks the next page holds.
	moved []unit.Unit
}

func (s *taskSet) move(units []unit.Unit) {
	for _, u := range units {
		if !slices.Contains(s.moved, u) {
			s.moved = append(s.moved, u)
		}
	}
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
