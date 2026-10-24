package tui

import (
	"strings"
	"time"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/termfmt"
)

// outboxList lays the drafts out: their age, target, text and state.
func (m *Model) outboxList(entries []api.EntryView, cur, width, height int, now time.Time) []string {
	t := m.t
	cols := []termfmt.Column{{Right: true}, {Max: min(max((width-2)/4, 12), 32)}, {Flex: true}}
	if width >= 80 {
		cols = append(cols, termfmt.Column{Max: 18})
	}
	cells := make([][]termfmt.Cell, len(entries))
	for i, e := range entries {
		age, style := m.entryAge(e, now)
		target, text := draftTarget(e), oneLine(e.Title)
		if d := e.Draft; d != nil {
			text = oneLine(d.Text)
		}
		c := []termfmt.Cell{{Text: age, Style: style}, {Text: target}, {Text: text}}
		if len(cols) > 3 {
			c = append(c, termfmt.Cell{Text: draftState(e), Style: t.Dim})
		}
		cells[i] = c
	}
	laid := t.Columns(width-3, 2, cols, cells)
	out := make([]string, len(entries))
	for i, e := range entries {
		out[i] = m.entryLine(e, laid[i], i == cur, width)
	}
	return window(out, cur, height)
}

// draftTarget is where the draft goes as its publisher named it, else its raw target or unit, marked
// unknown while the draft is still to be posted.
func draftTarget(e api.EntryView) string {
	d := e.Draft
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
func draftState(e api.EntryView) string {
	d := e.Draft
	if d == nil {
		return e.State
	}
	parts := []string{d.State}
	if d.Approval.Mode != "" && d.Approval.Mode != "always" {
		parts = append(parts, d.Approval.Mode)
	}
	return strings.Join(parts, " · ")
}

// draftFacts adds the draft's target, its whole text, kept in its
// paragraphs, and why it failed or what it would do.
func (m *Model) draftFacts(d *api.Draft, fact func(label, text string, style func(string) string), para func(label, text string)) {
	t := m.t
	fact("to", draftTarget(api.EntryView{Draft: d}), plain)
	head := d.Kind
	if d.Edited {
		head += " · edited"
	}
	if d.Approval.Mode != "" {
		head += " · approval " + d.Approval.Mode
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
	if d.Would.Outcome != "" {
		fact("would", strings.TrimSuffix(d.Would.Outcome+": "+d.Would.Reason, ": "), t.Dim)
	}
}
