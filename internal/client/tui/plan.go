package tui

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/client/texts"
	"github.com/wloophq/wloop/internal/termfmt"
)

// planActs are the plan actions POST /api/plan carries, by their spec id;
// the other actions an item offers are its unit's.
var planActs = map[string]string{"plan_apply": "apply", "plan_dismiss": "dismiss", "guide": "guide"}

// shown are the plan's decisions the view lists, those that wait on the user.
var shown = []string{"pending", "blocked"}

// SetPlan replaces the plan items, pending ones whose window ends soonest
// first, and keeps the selected one while it is still pending or blocked,
// else the same place in the list.
func (m *Model) SetPlan(items []api.PlanItem, err error) {
	m.planLoaded = true
	if err != nil {
		m.planErr = m.w().T("plan not loaded: {error}", map[string]any{"error": err.Error()})
		return
	}
	m.planErr = ""
	m.plan = slices.DeleteFunc(slices.Clone(items), func(it api.PlanItem) bool { return !slices.Contains(shown, it.Decision) })
	slices.SortStableFunc(m.plan, func(a, b api.PlanItem) int {
		return cmp.Or(cmp.Compare(slices.Index(shown, a.Decision), slices.Index(shown, b.Decision)),
			cmp.Compare(waits(a), waits(b)), a.Deadline.Compare(b.Deadline))
	})
	m.keepPlanSelection()
}

// waits is 1 for an item that runs only when the user applies it.
func waits(it api.PlanItem) int {
	if it.Default == "run" {
		return 0
	}
	return 1
}

// SetClosed replaces the sessions closed in the last day.
func (m *Model) SetClosed(closed []api.ClosedSession) { m.closed = closed }

// SetPlanSpecs replaces the specs of the actions a plan item may offer.
func (m *Model) SetPlanSpecs(specs []api.ActionSpec) { m.planSpecs = specs }

func (m *Model) planCursor() int {
	if i := slices.IndexFunc(m.plan, func(it api.PlanItem) bool { return it.ID == m.planSel }); i >= 0 {
		return i
	}
	return min(max(m.planAt, 0), max(len(m.plan)-1, 0))
}

func (m *Model) keepPlanSelection() {
	if len(m.plan) == 0 {
		m.planSel = ""
		return
	}
	m.planAt = m.planCursor()
	m.planSel = m.plan[m.planAt].ID
}

func (m *Model) planMove(delta int) {
	if len(m.plan) == 0 {
		return
	}
	m.planAt = min(max(m.planCursor()+delta, 0), len(m.plan)-1)
	m.planSel = m.plan[m.planAt].ID
}

func (m *Model) selectedItem() (api.PlanItem, bool) {
	if len(m.plan) == 0 {
		return api.PlanItem{}, false
	}
	return m.plan[m.planCursor()], true
}

// keyName is how a spec names the key k: enter, or the rune typed.
func keyName(k Key) string {
	if k.Name == KeyEnter {
		return "Enter"
	}
	return string(k.Rune)
}

// planAction is the action k names on the selected item, as action is for
// a row.
func (m *Model) planAction(k Key) (Command, bool) {
	it, ok := m.selectedItem()
	if !ok {
		return Command{}, false
	}
	var spec *api.ActionSpec
	var st api.ActionState
	offered := false
	for i := range m.planSpecs {
		s := &m.planSpecs[i]
		if s.Key != keyName(k) || !slices.Contains(s.Surfaces, "tui") {
			continue
		}
		if spec == nil {
			spec = s
		}
		if st, offered = itemState(it, s.ID); offered {
			spec = s
			break
		}
	}
	if spec == nil {
		return Command{}, false
	}
	label := itemLabel(it)
	if running, busy := m.busy[it.Unit]; busy {
		m.status = m.w().T("{action} {task} still running", map[string]any{"action": m.actionWord(running), "task": label})
		return Command{}, false
	}
	switch {
	case !offered:
		m.status = m.w().T("no {action} on {task}", map[string]any{"action": strings.ToLower(spec.Label), "task": label})
	case !st.Enabled:
		m.status = st.Reason
	default:
		c := Command{Unit: it.Unit, Label: label, Action: spec.ID, Req: api.ActionRequest{Source: Source, Basis: st.Basis}}
		if _, ok := planActs[spec.ID]; ok {
			c.PlanID = it.ID
		}
		if spec.Input == nil {
			return m.finish(c, st, k.Read)
		}
		m.ask(*spec, st, c, inputLabel(m.w(), label, *spec, st), k.Read)
	}
	return Command{}, false
}

func itemState(it api.PlanItem, id string) (api.ActionState, bool) {
	for _, st := range it.Actions {
		if st.ID == id {
			return st, true
		}
	}
	return api.ActionState{}, false
}

// itemLabel names an item on the status line: its workflow, and its unit
// by label and title.
func itemLabel(it api.PlanItem) string {
	label := it.Workflow + " " + oneLine(it.UnitLabel())
	if title := oneLine(it.Title); title != "" {
		label += " · " + title
	}
	return label
}

// timeLeft is the item's list column and its color: the time left on a
// pending item's window, wait when it waits for the user, blocked.
func (m *Model) timeLeft(it api.PlanItem, now time.Time) (string, func(string) string) {
	t := m.t
	switch {
	case it.Decision == "blocked":
		return m.w().T("blocked", nil), t.Red
	case it.Default != "run":
		return m.w().T("wait", nil), t.Yellow
	case it.Deadline.IsZero():
		return m.w().T("now", nil), t.Cyan
	}
	return fmt.Sprintf("%ds", max(0, int(it.Deadline.Sub(now).Seconds()+0.999))), t.Cyan
}

func (m *Model) renderPlan(lines []string, width, body int, now time.Time) []string {
	if m.planErr != "" {
		return append(lines, "  "+m.t.Red(termfmt.Cut(m.planErr, width-2)))
	}
	closed := m.closedLines(width, now)
	room := body - 2
	if len(m.plan) > 0 {
		room = body / 2
	}
	if len(closed) > room {
		closed = append(closed[:max(room-1, 1)], "  "+m.t.Dim(termfmt.Ellipsis))
	}
	if len(closed) > 0 {
		body -= len(closed) + 1
	}
	if len(m.plan) == 0 {
		text := m.w().T("Nothing pending or blocked in the plan.", nil)
		if !m.planLoaded {
			text = m.w().T("loading…", nil)
		}
		lines = append(lines, "  "+m.t.Dim(text))
	} else {
		lines = m.renderItems(lines, width, body, now)
	}
	if len(closed) > 0 {
		lines = append(append(lines, ""), closed...)
	}
	return lines
}

// closedLines are the sessions closed in the last day, each with its age,
// reason and the line that resumes it.
func (m *Model) closedLines(width int, now time.Time) []string {
	if len(m.closed) == 0 {
		return nil
	}
	t := m.t
	out := []string{"  " + t.Bold(m.w().T("Sessions closed in the last day ({n})", map[string]any{"n": len(m.closed)}))}
	for _, c := range m.closed {
		out = append(out, "  "+termfmt.Cut(fmt.Sprintf("%s  %s  %s", span(now.Sub(c.At)), c.Session, oneLine(c.Reason)), width-2))
		if c.Resume != "" {
			out = append(out, "    "+t.Dim(termfmt.Cut(m.w().T("resume: {command}", map[string]any{"command": oneLine(c.Resume)}), width-4)))
		}
	}
	return out
}

func (m *Model) renderItems(lines []string, width, body int, now time.Time) []string {
	cur := m.planCursor()
	var detail []string
	if width >= 40 && body >= 8 {
		detail = m.planDetail(m.plan[cur], width, now)
		room := body - min(len(m.plan), max(body/2, 3)) - 1
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
	lines = append(lines, m.planList(cur, width, listHeight, now)...)
	if len(detail) > 0 {
		lines = append(append(lines, ""), detail...)
	}
	return lines
}

func (m *Model) planList(cur, width, height int, now time.Time) []string {
	t := m.t
	unitMax := min(max((width-2)*2/5, 14), 60)
	cols := []termfmt.Column{{Right: true}, {Max: 28}, {Max: unitMax}, {Flex: true}}
	cells := make([][]termfmt.Cell, len(m.plan))
	for i, it := range m.plan {
		left, style := m.timeLeft(it, now)
		name := shortRef(it.UnitLabel(), min(unitMax, 40))
		if it.Title != "" {
			name += "  " + oneLine(it.Title)
		}
		link := func(s string) string { return t.Link(it.Link, s) }
		cells[i] = []termfmt.Cell{{Text: left, Style: style}, {Text: firstOf(it.Summary, it.Workflow), Style: t.Dim}, {Text: name, Style: link}, {Text: oneLine(m.planReason(it))}}
	}
	laid := t.Columns(width-3, 2, cols, cells)
	start := max(0, cur-height+1)
	var out []string
	for i := start; i < len(m.plan) && i < start+height; i++ {
		mark := []byte("   ")
		if i == cur {
			mark[0] = '>'
		}
		if _, busy := m.busy[m.plan[i].Unit]; busy {
			mark[1] = '*'
		}
		line := string(mark) + laid[i]
		if i == cur && t.Color {
			line = t.Reverse(line + strings.Repeat(" ", max(width-termfmt.Cells(termfmt.Plain(line)), 0)))
		}
		out = append(out, line)
	}
	return out
}

// planDetail is the selected item: its unit, its workflow, its decision and
// time left, the reason, the steps, the facts that matched, what it frees
// and the keys it offers.
func (m *Model) planDetail(it api.PlanItem, width int, now time.Time) []string {
	t := m.t
	label := termfmt.Cut(it.UnitLabel(), width)
	head := t.Bold(label)
	if room := width - termfmt.Cells(label) - 2; room > 0 && it.Title != "" {
		head += "  " + termfmt.Cut(oneLine(it.Title), room)
	}
	lines := []string{head}
	col := 2 + factLabel + 2
	room := width - col
	fact := func(name, text string, style func(string) string) {
		for i, l := range termfmt.Wrap(oneLine(text), room, room) {
			if i == 0 {
				lines = append(lines, "  "+t.Dim(termfmt.Fit(name, factLabel))+"  "+style(l))
				continue
			}
			lines = append(lines, strings.Repeat(" ", col)+style(l))
		}
	}
	decision := it.Decision
	switch left, _ := m.timeLeft(it, now); {
	case it.Decision != "pending":
	case it.Default != "run":
		decision += " · " + m.w().T("waits for you", nil)
	default:
		decision += " · " + m.w().T("runs in {time}", map[string]any{"time": left})
	}
	if it.Observe {
		decision += " · " + m.w().T("observe", nil)
	}
	fact(m.w().T("workflow", nil), firstOf(it.Summary, it.Workflow), plain)
	fact(m.w().T("decision", nil), decision, plain)
	if it.Reason != "" {
		fact(m.w().T("why", nil), m.planReason(it), plain)
	}
	if len(it.Does) == 0 {
		fact(m.w().T("steps", nil), strings.Join(it.Steps, ", "), plain)
	}
	for _, st := range it.Does {
		fact(m.w().T("steps", nil), m.stepText(st), plain)
		for _, item := range st.Items {
			fact("", "· "+item, t.Dim)
		}
	}
	if len(it.Does) > 0 && !planPosts(it.Does) {
		fact("", m.w().T("Posts nothing", nil), t.Dim)
	}
	var matched []string
	for _, f := range it.Why.Matched {
		matched = append(matched, m.planFactText(f))
	}
	if len(matched) > 0 {
		fact(m.w().T("matched", nil), strings.Join(matched, ", "), t.Dim)
	}
	if g := gain(m.w(), it.Gain); g != "" {
		fact(m.w().T("frees", nil), g, plain)
	}
	if it.Error != "" {
		fact(m.w().T("error", nil), m.w().T("attempt {n}: {error}", map[string]any{"n": it.Attempts, "error": it.Error}), t.Red)
	}
	for _, st := range it.Actions {
		if st.Enabled && st.Note != "" {
			fact(strings.ToLower(m.planSpecLabel(st)), st.Note, t.Dim)
		}
	}
	return append(lines, m.itemKeys(it, width, col)...)
}

func gain(w *texts.Catalog, g api.Gain) string {
	var parts []string
	if g.MemoryBytes > 0 {
		parts = append(parts, w.T("{size} of memory", map[string]any{"size": mib(g.MemoryBytes)}))
	}
	if g.DiskBytes > 0 {
		parts = append(parts, w.T("{size} of disk", map[string]any{"size": mib(g.DiskBytes)}))
	}
	if len(parts) < 2 {
		return strings.Join(parts, "")
	}
	return w.T("{list} and {last}", map[string]any{"list": strings.Join(parts[:len(parts)-1], ", "), "last": parts[len(parts)-1]})
}

func mib(n int64) string {
	if n >= 1<<30 {
		return fmt.Sprintf("%.1f GiB", float64(n)/(1<<30))
	}
	return fmt.Sprintf("%.1f MiB", float64(n)/(1<<20))
}

// planSpecLabel names an item's action as it is offered: its state's label, else its spec's.
func (m *Model) planSpecLabel(st api.ActionState) string {
	if st.Label != "" {
		return st.Label
	}
	if i := slices.IndexFunc(m.planSpecs, func(s api.ActionSpec) bool { return s.ID == st.ID }); i >= 0 {
		return cmp.Or(m.planSpecs[i].Label, st.ID)
	}
	return st.ID
}

// itemKeys lists the item's keyed actions as offered does a row's.
func (m *Model) itemKeys(it api.PlanItem, width, col int) []string {
	t := m.t
	on, off := m.planHelp(it)
	var lines []string
	head := "  " + t.Dim(termfmt.Fit(m.w().T("keys", nil), factLabel)) + "  "
	for _, l := range m.pack(on, width-col) {
		lines = append(lines, head+l)
		head = strings.Repeat(" ", col)
	}
	if len(off) > 0 {
		lines = append(lines, "  "+t.Dim(termfmt.Fit(m.w().T("not now", nil), factLabel))+"  "+t.Dim(termfmt.Cut(strings.Join(off, " · "), width-col)))
	}
	return lines
}

// planHelp are the item's enabled keyed actions, and its disabled ones.
func (m *Model) planHelp(it api.PlanItem) (on []helpItem, off []string) {
	for _, s := range m.planSpecs {
		st, ok := itemState(it, s.ID)
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
	return on, off
}
