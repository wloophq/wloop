package tui

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/termfmt"
)

const planLabel = "Plan"

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
		m.planErr = "plan not loaded: " + err.Error()
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
		m.status = fmt.Sprintf("%s %s still running", running, label)
		return Command{}, false
	}
	switch {
	case !offered:
		m.status = fmt.Sprintf("no %s on %s", strings.ToLower(spec.Label), label)
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
		m.ask(*spec, st, c, inputLabel(label, *spec, st), k.Read)
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

// itemLabel names an item on the status line: its workflow and unit.
func itemLabel(it api.PlanItem) string { return it.Workflow + " " + it.UnitLabel() }

// timeLeft is the item's list column and its color: the time left on a
// pending item's window, wait when it waits for the user, blocked.
func (m *Model) timeLeft(it api.PlanItem, now time.Time) (string, func(string) string) {
	t := m.t
	switch {
	case it.Decision == "blocked":
		return "blocked", t.Red
	case it.Default != "run":
		return "wait", t.Yellow
	case it.Deadline.IsZero():
		return "now", t.Cyan
	}
	return fmt.Sprintf("%ds", max(0, int(it.Deadline.Sub(now).Seconds()+0.999))), t.Cyan
}

func (m *Model) renderPlan(lines []string, width, body int, now time.Time) []string {
	if m.planErr != "" {
		return append(lines, "  "+m.t.Red(termfmt.Cut(m.planErr, width-2)))
	}
	if len(m.plan) == 0 {
		text := "Nothing pending or blocked in the plan."
		if !m.planLoaded {
			text = "loading…"
		}
		return append(lines, "  "+m.t.Dim(text))
	}
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
	cols := []termfmt.Column{{Right: true}, {Max: 28}, {Max: min(max((width-2)/3, 14), 40)}, {Flex: true}}
	cells := make([][]termfmt.Cell, len(m.plan))
	for i, it := range m.plan {
		left, style := m.timeLeft(it, now)
		cells[i] = []termfmt.Cell{{Text: left, Style: style}, {Text: it.Workflow, Style: t.Dim}, {Text: shortRef(it.UnitLabel(), 40)}, {Text: oneLine(it.Reason)}}
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

// planDetail is the selected item: its decision and time left, the reason,
// the steps, the facts that matched, what it frees and the keys it offers.
func (m *Model) planDetail(it api.PlanItem, width int, now time.Time) []string {
	t := m.t
	label := termfmt.Cut(it.UnitLabel(), width)
	head := t.Bold(label)
	if room := width - termfmt.Cells(label) - 2; room > 0 {
		head += "  " + termfmt.Cut(it.Workflow, room)
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
	case left == "wait":
		decision += " · waits for you"
	default:
		decision += " · runs in " + left
	}
	if it.Mode == "observe" {
		decision += " · observe"
	}
	fact("decision", decision, plain)
	if it.Reason != "" {
		fact("why", it.Reason, plain)
	}
	fact("steps", strings.Join(it.Steps, ", "), plain)
	var matched []string
	for _, f := range it.Why.Matched {
		matched = append(matched, f.Name+" = "+f.Value)
	}
	if len(matched) > 0 {
		fact("matched", strings.Join(matched, ", "), t.Dim)
	}
	if g := gain(it.Gain); g != "" {
		fact("frees", g, plain)
	}
	if it.Error != "" {
		fact("error", fmt.Sprintf("attempt %d: %s", it.Attempts, it.Error), t.Red)
	}
	return append(lines, m.itemKeys(it, width, col)...)
}

func gain(g api.Gain) string {
	var parts []string
	if g.MemoryBytes > 0 {
		parts = append(parts, mib(g.MemoryBytes)+" of memory")
	}
	if g.DiskBytes > 0 {
		parts = append(parts, mib(g.DiskBytes)+" of disk")
	}
	return strings.Join(parts, " and ")
}

func mib(n int64) string {
	if n >= 1<<30 {
		return fmt.Sprintf("%.1f GiB", float64(n)/(1<<30))
	}
	return fmt.Sprintf("%.1f MiB", float64(n)/(1<<20))
}

// itemKeys lists the item's keyed actions as offered does a row's.
func (m *Model) itemKeys(it api.PlanItem, width, col int) []string {
	t := m.t
	on, off := m.planHelp(it)
	var lines []string
	head := "  " + t.Dim(termfmt.Fit("keys", factLabel)) + "  "
	for _, l := range m.pack(on, width-col) {
		lines = append(lines, head+l)
		head = strings.Repeat(" ", col)
	}
	if len(off) > 0 {
		lines = append(lines, "  "+t.Dim(termfmt.Fit("not now", factLabel))+"  "+t.Dim(termfmt.Cut(strings.Join(off, " · "), width-col)))
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
