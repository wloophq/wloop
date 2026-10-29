package tui

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/wloophq/wloop/internal/api"
	dtask "github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/termfmt"
)

// ctxReview is the key context of the review, a mode of the terminal that walks the catalog's review steps; the
// served view of that name is where it sits among the views.
const ctxReview = "review"

// projectHandlers are the handlers of a project's verbs, which no key takes: the palette runs them.
var projectHandlers = []string{"reviewed", "sequential"}

// reviewView is the weekly Review on screen: the daemon's Review, the step
// shown, and whether the cursor is on that step's header rather than on one
// of its tasks.
type reviewView struct {
	review *api.Review
	step   string
	head   bool
}

func (m *Model) inReview() bool {
	v, ok := m.viewOnScreen()
	return ok && v.ID == ctxReview
}

// SetReview takes the Review the daemon serves; with no step shown, or one
// it no longer has, the first open step is shown.
func (m *Model) SetReview(r api.Review) {
	m.rv.review = &r
	if _, ok := m.reviewStep(); !ok {
		m.rv.step, m.rv.head = firstOpenStep(r.Steps, -1), true
	}
	m.keepTaskSelection()
}

// reviewStep is the step on screen.
func (m *Model) reviewStep() (api.ReviewStep, bool) {
	if m.rv.review == nil {
		return api.ReviewStep{}, false
	}
	i := slices.IndexFunc(m.rv.review.Steps, func(s api.ReviewStep) bool { return s.ID == m.rv.step })
	if i < 0 {
		return api.ReviewStep{}, false
	}
	return m.rv.review.Steps[i], true
}

// firstOpenStep is the first step not done after index from, wrapping; the
// last step when every one is done.
func firstOpenStep(steps []api.ReviewStep, from int) string {
	for i := 1; i <= len(steps); i++ {
		if s := steps[(from+i)%len(steps)]; !s.Done {
			return s.ID
		}
	}
	if len(steps) == 0 {
		return ""
	}
	return steps[len(steps)-1].ID
}

// listView is the view whose tasks a task view lists: the step's on the
// Review.
func (m *Model) listView(v View) string {
	if v.ID != ctxReview {
		return v.ID
	}
	st, _ := m.reviewStep()
	return st.View
}

// reviewAction runs a key of the Review context; took is false where it
// does not apply, so Enter on a task opens it.
func (m *Model) reviewAction(action string) (c Command, ok, took bool) {
	st, shown := m.reviewStep()
	if !m.inReview() || !shown {
		return Command{}, false, false
	}
	steps := m.rv.review.Steps
	at := slices.IndexFunc(steps, func(s api.ReviewStep) bool { return s.ID == st.ID })
	switch action {
	case "step:next", "step:prev":
		next := at + 1
		if action == "step:prev" {
			next = at - 1
		}
		m.showStep(steps[min(max(next, 0), len(steps)-1)].ID)
		return Command{}, false, true
	case "step:done":
		if !m.rv.head {
			return Command{}, false, false
		}
		if st.Done {
			m.status = st.Label + " is already done"
			return Command{}, false, true
		}
		steps[at].Done = true
		m.rv.review.Done = !slices.ContainsFunc(steps, func(s api.ReviewStep) bool { return !s.Done })
		if !m.rv.review.Done {
			m.showStep(firstOpenStep(steps, at))
		}
		c, ok = m.send(Command{Unit: taskUnit(ctxReview), Label: st.Label, Action: "review", reviewStep: st.ID})
		return c, ok, true
	}
	return Command{}, false, false
}

func (m *Model) showStep(id string) {
	m.rv.step, m.rv.head, m.taskAt, m.taskSel = id, true, 0, ""
	m.closeDetail()
}

// reviewMove moves between the step's header and its tasks; false leaves
// the move to the task list.
func (m *Model) reviewMove(delta int) bool {
	if !m.inReview() {
		return false
	}
	tasks := m.visibleTasks()
	switch {
	case m.rv.head && delta > 0 && len(tasks) > 0:
		m.rv.head, m.taskAt, m.taskSel = false, 0, tasks[0].ID
		return true
	case m.rv.head:
		return true
	case delta < 0 && m.taskCursor(tasks) == 0:
		m.rv.head = true
		m.closeDetail()
		return true
	}
	return false
}

// renderReview draws the Review's steps, the step on screen's header and
// its tasks.
func (m *Model) renderReview(lines []string, width, body int, now time.Time) []string {
	t := m.t
	if m.rv.review == nil {
		return append(lines, "  "+t.Dim("loading…"))
	}
	start := len(lines)
	if body < len(m.rv.review.Steps)+8 {
		lines = append(lines, m.reviewLine(width))
	} else {
		lines = append(lines, m.reviewSteps(width)...)
	}
	if m.rv.review.Done {
		lines = append(lines, "  "+t.Dim(termfmt.Cut("Review done. A new one starts the next time you open it.", width-2)))
	}
	st, ok := m.reviewStep()
	if !ok {
		return lines
	}
	head := fmt.Sprintf("%s (%d)", st.Label, st.Count)
	if st.Done {
		head += " ✓"
	}
	mark := "   "
	if m.rv.head {
		mark = ">  "
	}
	line := mark + t.Bold(termfmt.Cut(head, width-len(mark)-1))
	if m.rv.head && t.Color {
		line = t.Reverse(line + strings.Repeat(" ", max(width-termfmt.Cells(termfmt.Plain(line)), 0)))
	}
	lines = append(lines, "", line)
	return m.renderTasks(lines, View{ID: st.View, Label: st.Label}, width, body-(len(lines)-start), now)
}

// reviewLine is the steps on one line, by id: a short screen's.
func (m *Model) reviewLine(width int) string {
	t := m.t
	var parts []string
	for _, s := range m.rv.review.Steps {
		mark := "○"
		if s.Done {
			mark = "✓"
		}
		part := fmt.Sprintf("%s %s %d", mark, s.ID, s.Count)
		if s.ID == m.rv.step {
			part = t.Bold("▸" + part)
		} else if s.Done {
			part = t.Dim(part)
		}
		parts = append(parts, part)
	}
	return " " + termfmt.Fit(strings.Join(parts, "  "), width-1)
}

// reviewSteps is one line per step: the one on screen, done or not, its
// label and count.
func (m *Model) reviewSteps(width int) []string {
	t := m.t
	cells := make([][]termfmt.Cell, len(m.rv.review.Steps))
	for i, s := range m.rv.review.Steps {
		mark, style := "○", plain
		if s.Done {
			mark, style = "✓", t.Dim
		}
		cur := " "
		if s.ID == m.rv.step {
			cur = "▸"
		}
		cells[i] = []termfmt.Cell{{Text: cur}, {Text: mark, Style: style}, {Text: s.Label, Style: style}, {Text: fmt.Sprint(s.Count), Style: t.Dim}}
	}
	cols := []termfmt.Column{{}, {}, {Flex: true}, {Right: true}}
	var lines []string
	for _, l := range t.Columns(min(width-2, 64), 1, cols, cells) {
		lines = append(lines, " "+l)
	}
	return lines
}

// reviewHelp are the Review's keys for the help line.
func (m *Model) reviewHelp() []helpItem {
	var items []helpItem
	if m.rv.head {
		items = append(items, helpItem{"Enter", "step done"})
	}
	return append(items, helpItem{"]", "next step"}, helpItem{"[", "previous step"})
}

// projectFacts are a project's review mark, open children, with no next
// action when it has none, and interval; none for another kind.
func projectFacts(e api.TaskView) []string {
	if e.PropText(dtask.PropKind) != dtask.KindProject {
		return nil
	}
	var out []string
	if e.ReviewDue {
		out = append(out, "review due")
	}
	if len(e.Children) == 0 {
		out = append(out, "no next action")
	}
	out = append(out, fmt.Sprintf("%d open", len(e.Children)))
	if d, err := time.ParseDuration(e.ReviewEvery); err == nil && d > 0 {
		out = append(out, "every "+everyLabel(d))
	}
	return out
}

func everyLabel(d time.Duration) string {
	if d%(24*time.Hour) == 0 {
		return fmt.Sprintf("%dd", d/(24*time.Hour))
	}
	return d.String()
}
