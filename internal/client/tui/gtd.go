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

// entryProvider names an entry in the busy map, which is keyed by unit.
const entryProvider unit.ProviderID = "entry"

func entryUnit(id string) unit.Unit { return unit.Unit{Provider: entryProvider, Key: id} }

// SetGTD takes the core views of GET /api/gtd. The first time, before the
// user moved to a view, it shows Focus.
func (m *Model) SetGTD(c api.GTDCatalog) {
	current := m.viewKey()
	if !m.navigated {
		current = "gtd:" + viewFocus
	}
	m.gtdCat, m.gtd = c, nil
	for _, v := range c.Views {
		m.gtd = append(m.gtd, View{ID: v.ID, Label: v.Label})
	}
	m.goTo(current)
}

// SetEntries replaces the entries of view, as its last page left them, and
// the counters that page carried.
func (m *Model) SetEntries(view string, entries []api.EntryView, c api.Counters) {
	if m.entries == nil {
		m.entries = map[string][]api.EntryView{}
	}
	m.entries[view] = entries
	m.entriesLoaded = true
	m.SetCounters(c)
	if d := m.opened; d != nil {
		if i := slices.IndexFunc(entries, func(e api.EntryView) bool { return e.ID == d.ID }); i >= 0 && entries[i].Updated.After(d.Updated) {
			m.detailStale = true
		}
	}
	m.keepEntrySelection()
}

// SetCounters replaces the counts the tabs show.
func (m *Model) SetCounters(c api.Counters) {
	if c.Area == m.area {
		m.counters = c
	}
}

// gtdBase is the index of the first GTD view, after the plan.
func (m *Model) gtdBase() int { return m.planView() + 1 }

// gtdView is the GTD view on screen.
func (m *Model) gtdView() (View, bool) {
	if i := m.view - m.gtdBase(); i >= 0 && i < len(m.gtd) {
		return m.gtd[i], true
	}
	return View{}, false
}

// EntryQuery is the GTD view on screen and the area the list follows; ok
// is false outside the GTD views.
func (m *Model) EntryQuery() (view, area string, ok bool) {
	v, ok := m.gtdView()
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
	gtd := i >= m.gtdBase() && i < m.pluginBase()
	for j := range m.viewCount() {
		if (j >= m.gtdBase() && j < m.pluginBase()) == gtd {
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

// visibleEntries are the entries of the GTD view on screen the filter
// keeps, in the order the list shows them: grouped by context in Next and
// by person in Waiting.
func (m *Model) visibleEntries() []api.EntryView {
	v, ok := m.gtdView()
	if !ok {
		return nil
	}
	q, view := string(m.filter), m.listView(v)
	var out []api.EntryView
	for _, e := range m.entries[view] {
		if entryMatches(e, q) {
			out = append(out, e)
		}
	}
	if key := m.grouping(view); key != nil {
		order := m.groupOrder(out, key)
		slices.SortStableFunc(out, func(a, b api.EntryView) int {
			return cmp.Compare(slices.Index(order, key(a)), slices.Index(order, key(b)))
		})
	}
	return out
}

// grouping is the key a view groups its entries by, nil when it does not.
func (m *Model) grouping(view string) func(api.EntryView) string {
	switch view {
	case viewNext:
		return func(e api.EntryView) string {
			if len(e.Contexts) == 0 {
				return ""
			}
			return e.Contexts[0]
		}
	case viewWaiting:
		return func(e api.EntryView) string {
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
func (m *Model) groupOrder(entries []api.EntryView, key func(api.EntryView) string) []string {
	seen := map[string]bool{}
	var known, other []string
	empty := false
	for _, e := range entries {
		k := key(e)
		switch {
		case seen[k]:
		case k == "":
			empty = true
		case slices.Contains(m.gtdCat.Contexts, k):
			known = append(known, k)
		default:
			other = append(other, k)
		}
		seen[k] = true
	}
	slices.SortFunc(known, func(a, b string) int {
		return cmp.Compare(slices.Index(m.gtdCat.Contexts, a), slices.Index(m.gtdCat.Contexts, b))
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

// entryMatches reports whether every word of query is in one of the
// entry's texts, as Matches does for a row.
func entryMatches(e api.EntryView, query string) bool {
	if strings.TrimSpace(query) == "" {
		return true
	}
	r := api.Row{Title: e.Title}
	extra := []string{e.Notes, e.Area, e.ProjectTitle, e.State, e.Kind}
	extra = append(extra, e.Contexts...)
	for i := range e.Units {
		extra = append(extra, e.UnitLabel(i))
	}
	if w := e.Waiting; w != nil {
		for _, p := range w.On {
			extra = append(extra, p.Name)
		}
	}
	for _, it := range e.Items {
		extra = append(extra, it.Summary)
	}
	for _, s := range e.Cards {
		for _, p := range s.Card.WaitsOn {
			extra = append(extra, p.Name)
		}
		if h := s.Card.LastHuman; h != nil {
			extra = append(extra, h.Who.Name)
		}
	}
	if d := e.Draft; d != nil {
		extra = append(extra, d.Target, d.Where, d.Text)
	}
	if msg := e.Message; msg != nil {
		extra = append(extra, msg.Text)
	}
	return Matches(r, query, "", extra...)
}

func (m *Model) entryCursor(entries []api.EntryView) int {
	if i := slices.IndexFunc(entries, func(e api.EntryView) bool { return e.ID == m.entrySel }); i >= 0 {
		return i
	}
	return min(max(m.entryAt, 0), max(len(entries)-1, 0))
}

func (m *Model) keepEntrySelection() {
	if _, ok := m.gtdView(); !ok {
		return
	}
	entries := m.visibleEntries()
	if len(entries) == 0 {
		m.entrySel = ""
		return
	}
	m.entryAt = m.entryCursor(entries)
	m.entrySel = entries[m.entryAt].ID
}

func (m *Model) selectedEntry() (api.EntryView, bool) {
	entries := m.visibleEntries()
	if len(entries) == 0 || (m.rv.head && m.inReview()) {
		return api.EntryView{}, false
	}
	return entries[m.entryCursor(entries)], true
}

func (m *Model) entryMove(delta int) {
	entries := m.visibleEntries()
	if len(entries) == 0 {
		return
	}
	m.entryAt = min(max(m.entryCursor(entries)+delta, 0), len(entries)-1)
	m.entrySel = entries[m.entryAt].ID
	m.closeDetail()
}

// entryLabel names an entry on the status line.
func entryLabel(e api.EntryView) string {
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
func (m *Model) sourceLink(e api.EntryView) string {
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

// renderEntries draws the GTD view on screen: its entries, the open
// detail below them.
func (m *Model) renderEntries(lines []string, v View, width, body int, now time.Time) []string {
	entries := m.visibleEntries()
	if len(entries) == 0 {
		text := "Nothing in " + v.Label + " right now."
		switch {
		case !m.entriesLoaded:
			text = "loading…"
		case len(m.filter) > 0:
			text = "Nothing in " + v.Label + " matches \"" + string(m.filter) + "\"."
		}
		return append(lines, "  "+m.t.Dim(termfmt.Cut(text, width-2)))
	}
	cur := m.entryCursor(entries)
	if m.rv.head && m.inReview() {
		cur = -1
	}
	var detail []string
	if cur >= 0 && m.detailOpen(entries[cur]) && width >= 40 && body >= 8 {
		detail = m.entryDetail(m.shownDetail(entries[cur]), width, now)
		room := body - min(len(entries), max(body/3, 3)) - 1
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
	lines = append(lines, m.entryList(v.ID, entries, cur, width, listHeight, now)...)
	if len(detail) > 0 {
		lines = append(append(lines, ""), detail...)
	}
	return lines
}

// entryList lays the entries out in columns, with a head line before each
// group, and shows the height of them around the cursor.
func (m *Model) entryList(view string, entries []api.EntryView, cur, width, height int, now time.Time) []string {
	t := m.t
	if view == viewOutbox {
		return m.outboxList(entries, cur, width, height, now)
	}
	cols := []termfmt.Column{{Right: true}, {Flex: true}}
	if width >= 60 {
		cols = append(cols, termfmt.Column{Max: min(max((width-2)/3, 14), 40)})
	}
	cells := make([][]termfmt.Cell, len(entries))
	for i, e := range entries {
		age, style := m.entryAge(e, now)
		title := oneLine(e.Title)
		if e.Focus {
			title = "★ " + title
		}
		c := []termfmt.Cell{{Text: age, Style: style}, {Text: title}}
		if len(cols) > 2 {
			c = append(c, termfmt.Cell{Text: m.entryWhere(e), Style: t.Dim})
		}
		cells[i] = c
	}
	laid := t.Columns(width-3, 2, cols, cells)
	key := m.grouping(view)
	var out []string
	var at []int
	for i, e := range entries {
		if key != nil && (i == 0 || key(entries[i-1]) != key(e)) {
			out, at = append(out, " "+t.Bold(termfmt.Cut(groupHead(view, key(e)), width-1))), append(at, -1)
		}
		out, at = append(out, m.entryLine(e, laid[i], i == cur, width)), append(at, i)
	}
	return window(out, slices.Index(at, cur), height)
}

// entryLine marks the cursor, a busy entry and a changed one.
func (m *Model) entryLine(e api.EntryView, laid string, selected bool, width int) string {
	t := m.t
	mark := []rune("   ")
	if selected {
		mark[0] = '>'
	}
	if _, busy := m.busy[entryUnit(e.ID)]; busy {
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

// entryAge is how long the entry waits: since its waiting started, else
// since it last changed.
func (m *Model) entryAge(e api.EntryView, now time.Time) (string, func(string) string) {
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

// entryWhere is the entry's contexts and its first unit, or its project.
func (m *Model) entryWhere(e api.EntryView) string {
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

// gtdTabs are the GTD views with their counts.
func (m *Model) gtdTabs() []string {
	out := make([]string, len(m.gtd))
	for i, v := range m.gtd {
		if n, ok := m.counters.Views[v.ID]; ok {
			out[i] = fmt.Sprintf("%s %d", v.Label, n)
			continue
		}
		out[i] = v.Label
	}
	return out
}

// entrySet is one view's entries as the pages left them, in the daemon's
// order, and the position to pull the next page from.
type entrySet struct {
	since api.Since
	order []string
	byID  map[string]api.EntryView
}

// apply takes a page: false when it is a delta the set cannot take, from
// another epoch or before any full page. An older delta is ignored.
func (s *entrySet) apply(p api.EntriesPage) bool {
	switch {
	case p.Full:
		s.order, s.byID = nil, map[string]api.EntryView{}
	case s.since.Epoch == "" || p.Epoch != s.since.Epoch:
		return false
	case p.Rev < s.since.Rev:
		return true
	}
	for _, e := range p.Entries {
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

func (s *entrySet) list() []api.EntryView {
	out := make([]api.EntryView, len(s.order))
	for i, id := range s.order {
		out[i] = s.byID[id]
	}
	return out
}
