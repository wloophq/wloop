package tui

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/api/lens"
	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/termfmt"
)

var navHelp = []helpItem{{"↑↓", "move"}, {"g", "view"}, {"/", "filter"}, {"^K", "actions"}}

// lensHelp are the keys of the lens, which follow the filter on the lists.
var lensHelp = []helpItem{{"=", "group"}, {"-", "sort"}}

var quitHelp = helpItem{"q", "quit"}

var keysHelp = helpItem{"?", "keys"}

// staleMark marks a row its plugin last reported before it went down.
const staleMark = "(stale)"

// factLabel is the width of the detail pane's labels.
const factLabel = 9

// Render draws the screen as height lines of at most width cells: the
// views, the list of the current one, the selected pull request's detail,
// a status line and the help or prompt line.
func (m *Model) Render(width, height int, now time.Time) []string {
	m.now = now
	if width < 20 || height < 5 {
		return pad([]string{termfmt.Fit(m.w().T("{name}: terminal too small", map[string]any{"name": brand.Name}), width)}, height)
	}
	lines := []string{m.tabs(width)}
	if height >= 7 {
		lines = append(lines, "")
	}
	body := height - len(lines) - 2
	if m.overlay != nil {
		lines = pad(m.renderOverlay(lines, width, body), height-2)
		return append(lines, m.statusLine(width), m.bottom(width))
	}
	if m.inReview() {
		lines = pad(m.renderReview(lines, width, body, now), height-2)
		return append(lines, m.statusLine(width), m.bottom(width))
	}
	if v, ok := m.viewOnScreen(); ok {
		lines = pad(m.renderTasks(lines, v, width, body, now), height-2)
		return append(lines, m.statusLine(width), m.bottom(width))
	}
	if m.view == m.planView() {
		lines = pad(m.renderPlan(lines, width, body, now), height-2)
		return append(lines, m.statusLine(width), m.bottom(width))
	}
	if l, ok := m.loadingLine(m.rowsLoading, width); ok {
		lines, body = append(lines, l), body-1
	}
	rows, heads := m.visibleRows()
	cur := m.cursor(rows)
	var detail []string
	if r, ok := m.selected(); ok && width >= 40 && body >= 8 {
		detail = m.detail(r, width, now)
		room := body - min(len(rows), max(body/2, 3)) - 1
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
	if len(rows) == 0 {
		lines = append(lines, "  "+m.t.Dim(termfmt.Cut(m.empty(), width-2)))
	} else {
		lines = append(lines, m.list(rows, heads, cur, width, listHeight, now)...)
	}
	if len(detail) > 0 {
		lines = append(append(lines, ""), detail...)
	}
	lines = pad(lines, height-2)
	return append(lines, m.statusLine(width), m.bottom(width))
}

func pad(lines []string, n int) []string {
	n = max(n, 0)
	for len(lines) < n {
		lines = append(lines, "")
	}
	return lines[:n]
}

// tabs names the views with their counts, the current one bold, and the
// daemon's flags at the right edge when they fit: the group on screen
// whole, then each other group that fits whole, task views first.
func (m *Model) tabs(width int) string {
	t := m.t
	views := make([]tab, len(m.taskViews))
	for i, label := range m.viewTabs() {
		views[i] = tab{label, m.viewsBase() + i}
	}
	groups := [][]tab{views}
	at := map[string]int{}
	add := func(provider string, x tab) {
		i, ok := at[provider]
		if !ok {
			i = len(groups)
			at[provider] = i
			groups = append(groups, nil)
		}
		groups[i] = append(groups[i], x)
	}
	// The core views and the plan join the first plugin's row views.
	first := ""
	if i := slices.IndexFunc(m.views, func(v View) bool { return v.Provider != "" }); i >= 0 {
		first = m.views[i].Provider
	}
	for i, v := range m.views {
		shown, _ := m.in(v.ID)
		add(cmp.Or(v.Provider, first), tab{fmt.Sprintf("%s %d", v.Label, len(shown)), i})
	}
	add(first, tab{m.w().T("Plan {n}", map[string]any{"n": len(m.plan)}), m.planView()})
	mine := 0
	for i, g := range groups {
		if slices.ContainsFunc(g, func(x tab) bool { return x.view == m.view }) {
			mine = i
		}
	}
	used := tabCells(groups[mine])
	var shown []tab
	for i, g := range groups {
		if len(shown) > 0 && (i == mine || len(g) > 0 && used+tabCells(g)+sepCells <= width) {
			shown = append(shown, tab{label: "│", view: noView})
		}
		switch {
		case i == mine && used > width:
			shown = append(shown, around(g, m.view, width)...)
		case i == mine:
			shown = append(shown, g...)
		case len(g) > 0 && used+tabCells(g)+sepCells <= width:
			used += tabCells(g) + sepCells
			shown = append(shown, g...)
		}
	}
	var styled []string
	cur := ""
	for _, x := range shown {
		switch {
		case x.view != m.view:
			styled = append(styled, " "+t.Dim(x.label)+" ")
		case t.Color:
			cur = x.label
			styled = append(styled, t.Reverse(t.Bold(" "+x.label+" ")))
		default:
			cur = x.label
			styled = append(styled, "["+x.label+"]")
		}
	}
	line := strings.TrimRight(strings.Join(styled, "  "), " ")
	cells := termfmt.Cells(termfmt.Plain(line))
	if cells > width {
		return t.Bold(termfmt.Cut(cur, width))
	}
	var flags []string
	if m.area != "" {
		flags = append(flags, "area "+m.area)
	}
	if m.health.Paused {
		flags = append(flags, m.w().T("automatic rounds paused", nil))
	}
	if m.health.SlowSync {
		flags = append(flags, m.w().T("slow sync", nil))
	}
	if right := strings.Join(flags, " · "); right != "" {
		if gap := width - cells - termfmt.Cells(right); gap >= 3 {
			line += strings.Repeat(" ", gap) + t.Yellow(right)
		}
	}
	return line
}

// tab is a view's name on the tab line.
// noView is the view of a tab that is a group's separator, which takes
// sepCells with its joins.
const (
	noView   = -1
	sepCells = 7
)

type tab struct {
	label string
	view  int
}

// tabCells is about the width of g on the tab line.
func tabCells(g []tab) int {
	n := 0
	for _, x := range g {
		n += termfmt.Cells(x.label) + 4
	}
	return n
}

// around are the tabs of g next to view's that fit in width.
func around(g []tab, view, width int) []tab {
	i := slices.IndexFunc(g, func(x tab) bool { return x.view == view })
	if i < 0 {
		return g[:1]
	}
	lo, hi := i, i+1
	for grew := true; grew; {
		grew = false
		if hi < len(g) && tabCells(g[lo:hi+1]) <= width {
			hi, grew = hi+1, true
		}
		if lo > 0 && tabCells(g[lo-1:hi]) <= width {
			lo, grew = lo-1, true
		}
	}
	return g[lo:hi]
}

// list lays the view's rows out as lines, as status does, and shows the height of them around the cursor.
func (m *Model) list(rows []api.Row, heads []string, cur, width, height int, now time.Time) []string {
	lines := make([]listLine, len(rows))
	for i, r := range rows {
		lines[i] = m.rowLine(r, now)
	}
	return m.lineCells(lines, heads, cur, width, height)
}

// shortRef is a unit's label within w cells: a label of the form
// <scope>/<name>#<n> loses its scope when it does not fit, then has the
// name cut, never the #<n>; any other label is cut at its end.
func shortRef(label string, w int) string {
	label = termfmt.Clean(label)
	if termfmt.Cells(label) <= w {
		return label
	}
	hash := strings.LastIndex(label, "#")
	slash := strings.LastIndex(label[:max(hash, 0)], "/")
	if hash < 0 || slash <= 0 || slash+1 == hash || !digits(label[hash+1:]) || strings.ContainsAny(label, " \t") {
		return termfmt.Cut(label, w)
	}
	num := label[hash:]
	if room := w - termfmt.Cells(num); room >= 2 {
		return termfmt.Cut(label[slash+1:hash], room) + num
	}
	return termfmt.Cut(num, w)
}

// age is the row's age column and its color: a pending round's countdown,
// else how long it waits, marked ! and red once stalled, yellow from two
// days.
func (m *Model) age(r api.Row, now time.Time) (string, func(string) string) {
	t := m.t
	g, pending := m.roundGate(r)
	switch {
	case pending:
		return lens.Countdown(m.rowWordsOf(string(r.Unit.Provider)).RowWords, g.Deadline.Sub(now)), t.Cyan
	case r.Summary.Since.IsZero():
		return "-", t.Dim
	case r.Summary.Stalled:
		return "!" + span(now.Sub(r.Summary.Since)), t.Red
	case now.Sub(r.Summary.Since) >= 48*time.Hour:
		return span(now.Sub(r.Summary.Since)), t.Yellow
	}
	return span(now.Sub(r.Summary.Since)), t.Dim
}

// detail is the selected pull request: its title, its facts under aligned
// labels, the open question with its options and the actions it offers.
func (m *Model) detail(r api.Row, width int, now time.Time) []string {
	t := m.t
	ref := termfmt.Cut(r.Label, width)
	head := t.Bold(ref)
	if room := width - termfmt.Cells(ref) - 2; room > 0 && r.Title != "" {
		head += "  " + termfmt.Cut(oneLine(r.Title), room)
	}
	lines := []string{head}
	col := 2 + factLabel + 2
	room := width - col
	fact := func(label, text string, style func(string) string) {
		for i, l := range termfmt.Wrap(oneLine(text), room, room) {
			if i == 0 {
				lines = append(lines, "  "+t.Dim(termfmt.Fit(label, factLabel))+"  "+style(l))
				continue
			}
			lines = append(lines, strings.Repeat(" ", col)+style(l))
		}
	}
	age, _ := m.age(r, now)
	kind := m.kindLabel(r)
	waiting := m.waiting(r) + " · " + kind
	_, pending := m.roundGate(r)
	switch {
	case pending:
		waiting += " · " + m.w().T("round in {time}", map[string]any{"time": age})
	case r.Summary.Stalled:
		waiting += " · " + strings.TrimPrefix(age, "!") + ", stalled"
	case age != "-":
		waiting += " · " + age
	}
	fact("waiting", waiting, plain)
	if s := r.Summary; s.Line != "" && s.Line != s.Reason && s.Line != kind {
		fact("status", s.Line, plain)
	}
	if r.Summary.Reason != "" {
		fact("why", r.Summary.Reason, plain)
	}
	p := prOf(r)
	if b := p.Brief; b != nil {
		parts := []string{strings.ToUpper(b.Size)}
		if b.Risk != "" {
			parts = append(parts, b.Risk+" risk")
		}
		fact("brief", strings.Join(slices.DeleteFunc(append(parts, b.Goal), func(s string) bool { return s == "" }), " · "), plain)
	}
	if f := p.Facts; f != nil && len(f.Checks.Failing) > 0 {
		fact("checks", "failing: "+strings.Join(f.Checks.Failing, ", "), t.Red)
	}
	if where := strings.Join(slices.DeleteFunc([]string{p.Workspace.DisplayName, p.Workspace.Status}, func(s string) bool { return s == "" }), " · "); where != "" {
		fact("workspace", where, plain)
	}
	if q := r.Question; q != nil {
		m.question(q, fact)
	}
	return append(lines, m.offered(r, width, col)...)
}

// question adds the open question, the drafted verdict and the options.
func (m *Model) question(q *api.QuestionView, fact func(label, text string, style func(string) string)) {
	t := m.t
	if q.Queued {
		fact(m.w().T("answer", nil), queuedAnswer(m.w()), t.Dim)
		return
	}
	text := cmp.Or(q.Text, q.LastMessage, m.w().T("the agent stopped", nil))
	label := "asks"
	if q.Text == "" {
		label = "stopped"
	}
	fact(label, text, t.Yellow)
	if q.Verdict != "" {
		fact("", m.w().T("drafted {verdict}, {blocking} blocking, {nonblocking} non-blocking", map[string]any{"verdict": q.Verdict, "blocking": q.Blocking, "nonblocking": q.NonBlocking}), t.Dim)
	}
	if q.Kind == "confirm" {
		fact("", m.w().T("y yes · n no", nil), plain)
		return
	}
	for i, o := range q.Options {
		opt := fmt.Sprintf("%d. %s", i+1, o.Label)
		if o.ID == q.Recommend {
			opt += " (recommended)"
		}
		fact("", opt, plain)
	}
}

func plain(s string) string { return s }

// offered lists the row's keyed actions: the enabled ones packed after the
// "keys" label, then the disabled ones on one dimmed line; pressing one says
// why on the status line.
func (m *Model) offered(r api.Row, width, col int) []string {
	t := m.t
	var on []helpItem
	var off []string
	for _, s := range m.specs {
		st, ok := actionState(r, s.ID)
		if !ok || s.Key == "" || !slices.Contains(s.Surfaces, "tui") {
			continue
		}
		h := helpItem{s.Key, strings.ToLower(cmp.Or(st.Label, s.Label))}
		if st.Enabled {
			on = append(on, h)
		} else {
			off = append(off, h.String())
		}
	}
	var lines []string
	head := "  " + t.Dim(termfmt.Fit("keys", factLabel)) + "  "
	for _, l := range m.pack(on, width-col) {
		lines = append(lines, head+l)
		head = strings.Repeat(" ", col)
	}
	if len(off) > 0 {
		lines = append(lines, "  "+t.Dim(termfmt.Fit(m.w().T("not now", nil), factLabel))+"  "+t.Dim(termfmt.Cut(strings.Join(off, " · "), width-col)))
	}
	return lines
}

type helpItem struct{ key, label string }

func (h helpItem) String() string { return h.key + " " + h.label }

func (m *Model) styled(h helpItem) string { return m.t.Bold(h.key) + " " + m.t.Dim(h.label) }

// pack lays items two spaces apart in lines of at most room cells.
func (m *Model) pack(items []helpItem, room int) []string {
	var lines []string
	line, used := "", 0
	for _, h := range items {
		n := termfmt.Cells(h.String())
		if used > 0 && used+2+n > room {
			lines = append(lines, line)
			line, used = "", 0
		}
		if n > room {
			continue
		}
		if used > 0 {
			line, used = line+"  ", used+2
		}
		line, used = line+m.styled(h), used+n
	}
	if used > 0 {
		lines = append(lines, line)
	}
	return lines
}

// fill is a plain lead and as many items as fit in width, then ? keys
// when it fits and q quit.
func (m *Model) fill(lead string, items []helpItem, width int) string {
	tail := []helpItem{keysHelp, quitHelp}
	reserve := termfmt.Cells(keysHelp.String()) + 2 + termfmt.Cells(quitHelp.String())
	used := termfmt.Cells(lead)
	if used+reserve > width {
		tail, reserve = tail[1:], termfmt.Cells(quitHelp.String())
	}
	if used+reserve > width {
		return termfmt.Cut(lead, width)
	}
	line := lead
	for _, h := range items {
		n := termfmt.Cells(h.String())
		if used+n+2+reserve > width {
			break
		}
		line, used = line+m.styled(h)+"  ", used+n+2
	}
	styled := make([]string, len(tail))
	for i, h := range tail {
		styled[i] = m.styled(h)
	}
	return line + strings.Join(styled, "  ")
}

// who names the author as the cockpit does: "me" when it is the viewer.
func (m *Model) who(r api.Row) []string {
	who := strings.Split(r.Summary.WaitsOn, ", ")
	author := prOf(r).Author
	for i, w := range who {
		if w != "author" {
			continue
		}
		who[i] = author
		if m.health.Viewer != "" && strings.EqualFold(author, m.health.Viewer) {
			who[i] = "me"
		}
	}
	return who
}

func (m *Model) waiting(r api.Row) string { return strings.Join(m.who(r), ", ") }

// span is a duration as the cockpit's age column says it, in one unit.
func span(d time.Duration) string {
	switch {
	case d <= 0:
		return ""
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d/time.Minute))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh", int(d/time.Hour))
	}
	return fmt.Sprintf("%dd", int(d/(24*time.Hour)))
}

// oneLine is s cleaned with its whitespace runs collapsed to one space.
func oneLine(s string) string { return strings.Join(strings.Fields(termfmt.Clean(s)), " ") }

func (m *Model) empty() string {
	switch {
	case !m.loaded:
		return "loading…"
	case len(m.filter) > 0:
		return m.w().T("Nothing in {view} matches \"{filter}\".", map[string]any{"view": m.views[m.view].Label, "filter": string(m.filter)})
	}
	return m.w().T("Nothing in {view} right now.", map[string]any{"view": m.views[m.view].Label})
}

// statusLine is the last message: an error red, a warning yellow.
func (m *Model) statusLine(width int) string {
	t := m.t
	text, style := "", plain
	switch {
	case m.err != "":
		text, style = m.err, t.Red
	case m.status != "":
		text = m.status
	case len(m.health.Warnings) > 1:
		text, style = m.w().T("warning: {warning} (+{n} more)", map[string]any{"warning": m.health.Warnings[0], "n": len(m.health.Warnings) - 1}), t.Yellow
	case len(m.health.Warnings) == 1:
		text, style = m.w().T("warning: {warning}", map[string]any{"warning": m.health.Warnings[0]}), t.Yellow
	}
	if text = termfmt.Cut(text, width); text == "" {
		return ""
	}
	return style(text)
}

func (m *Model) bottom(width int) string {
	p := m.prompt
	switch {
	case p == nil && m.filtering:
		head, tail, text := "/", "  "+m.w().T("Enter keeps, Esc clears", nil), string(m.filter)+"_"
		room := width - termfmt.Cells(head) - termfmt.Cells(tail)
		if termfmt.Cells(text) > room && room < 8 {
			tail, room = "", width-termfmt.Cells(head)
		}
		if termfmt.Cells(text) > room && room > 1 {
			text = termfmt.Ellipsis + termfmt.Suffix(text, room-termfmt.Cells(termfmt.Ellipsis))
		}
		return termfmt.Cut(head+text+tail, width)
	case p == nil && len(m.filter) > 0:
		return m.fill(m.w().T("filter {text} (Esc clears)", map[string]any{"text": termfmt.Clean(string(m.filter))})+"  ", m.helpItems(), width)
	case p == nil:
		return m.fill("", m.helpItems(), width)
	case p.confirm:
		return termfmt.Cut(p.label+" "+m.w().T("y confirms, any other key cancels", nil), width)
	}
	head := p.label + " (" + m.w().T("Enter sends, Esc cancels", nil) + "): "
	text := string(p.text) + "_"
	if room := width - termfmt.Cells(head); termfmt.Cells(text) > room && room > 1 {
		text = termfmt.Ellipsis + termfmt.Suffix(text, room-termfmt.Cells(termfmt.Ellipsis))
	}
	return termfmt.Cut(head+text, width)
}

// helpItems are the keys of the actions the selected row offers now, then
// the moves.
func (m *Model) helpItems() []helpItem {
	var items []helpItem
	if _, ok := m.viewOnScreen(); ok {
		if m.inReview() {
			items = m.reviewHelp()
		}
		if e, ok := m.selectedTask(); ok {
			verbs, _ := m.verbHelp(e)
			items = append(items, verbs...)
		}
		return append(items, withLens(navHelp)...)
	}
	if m.view == m.planView() {
		if it, ok := m.selectedItem(); ok {
			items, _ = m.planHelp(it)
		}
		return append(items, navHelp...)
	}
	if r, ok := m.selected(); ok {
		for _, s := range m.specs {
			if st, ok := actionState(r, s.ID); ok && st.Enabled && s.Key != "" && slices.Contains(s.Surfaces, "tui") {
				items = append(items, helpItem{s.Key, strings.ToLower(cmp.Or(st.Label, s.Label))})
			}
		}
	}
	return append(items, withLens(navHelp)...)
}

// withLens is nav with the lens keys after the filter's.
func withLens(nav []helpItem) []helpItem {
	i := slices.IndexFunc(nav, func(h helpItem) bool { return h.key == "/" }) + 1
	return slices.Concat(nav[:i], lensHelp, nav[i:])
}

// help is the help line unstyled and uncut.
func (m *Model) help() string {
	var parts []string
	for _, h := range append(m.helpItems(), keysHelp, quitHelp) {
		parts = append(parts, h.String())
	}
	return strings.Join(parts, "  ")
}

func digits(s string) bool {
	return s != "" && strings.Trim(s, "0123456789") == ""
}
