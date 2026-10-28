package tui

import (
	"strings"
	"time"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/termfmt"
)

// outboxList lays the drafts out: their age, target, text and state.
func (m *Model) outboxList(tasks []api.TaskView, cur, width, height int, now time.Time) []string {
	t := m.t
	cols := []termfmt.Column{{Right: true}, {Max: min(max((width-2)/4, 12), 32)}, {Flex: true}}
	if width >= 80 {
		cols = append(cols, termfmt.Column{Max: 18})
	}
	cells := make([][]termfmt.Cell, len(tasks))
	for i, e := range tasks {
		age, style := m.taskAge(e, now)
		target, text := draftTarget(e), oneLine(e.Title)
		if d := draftOn(e); d != nil {
			text = oneLine(d.Text)
		}
		c := []termfmt.Cell{{Text: age, Style: style}, {Text: target}, {Text: text}}
		if len(cols) > 3 {
			c = append(c, termfmt.Cell{Text: draftState(e), Style: t.Dim})
		}
		cells[i] = c
	}
	laid := t.Columns(width-3, 2, cols, cells)
	out := make([]string, len(tasks))
	for i, e := range tasks {
		out[i] = m.taskLine(e, laid[i], i == cur, width)
	}
	return window(out, cur, height)
}

// draftTarget is where the draft goes as its publisher named it, else its raw target or unit, marked
// unknown while the draft is still to be posted.
func draftTarget(e api.TaskView) string {
	d := draftOn(e)
	if d != nil && d.Where != "" {
		return oneLine(d.Where)
	}
	raw := "-"
	switch {
	case d != nil && d.Target != "":
		raw = oneLine(d.Target)
	case len(e.Units) > 0:
		raw = e.UnitLabel(0)
	}
	if d != nil && d.Permalink == "" {
		raw += " (destination unknown)"
	}
	return raw
}

// draftState is the draft's state and how it is approved.
func draftState(e api.TaskView) string {
	d := draftOn(e)
	if d == nil {
		return e.State
	}
	parts := []string{d.State}
	if d.Deadline != nil {
		parts = append(parts, "sends "+d.Deadline.Local().Format("15:04"))
	}
	return strings.Join(parts, " · ")
}

// draftFacts adds the draft's target, its whole text, kept in its
// paragraphs, why it failed, and when its window sends it.
func (m *Model) draftFacts(d *api.Draft, fact func(label, text string, style func(string) string), para func(label, text string)) {
	t := m.t
	fact("to", draftTarget(draftTask(*d, "")), plain)
	head := d.Purpose
	if d.Edited {
		head += " · edited"
	}
	if d.Origin.Kind != "" {
		head += " · from " + d.Origin.Kind
	}
	fact("draft", strings.Trim(head, " ·"), t.Dim)
	para("text", d.Text)
	if d.Reaction != "" {
		fact("reaction", d.Reaction, plain)
	}
	if d.Error != "" {
		fact("error", d.Error, t.Red)
	}
	if d.Deadline != nil {
		fact("sends", d.Deadline.Local().Format("2006-01-02 15:04")+" unless you decide first", t.Dim)
	}
}
