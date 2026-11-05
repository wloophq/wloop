package tui

import (
	"strings"
	"time"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/api/lens"
)

// outboxList lays the drafts out on the list's line: how long since each last changed, its target as the ref,
// its text as the title, and its state as the kind.
func (m *Model) outboxList(tasks []api.TaskView, cur, width, height int, now time.Time) []string {
	words := m.viewsCat.Lens.RowWords
	lines := make([]listLine, len(tasks))
	for i, e := range tasks {
		text := oneLine(e.Title)
		if d := draftOn(e); d != nil {
			text = oneLine(d.Text)
		}
		l := listLine{label: draftTarget(e), title: text, changed: e.Changed, parts: map[string][]lens.Piece{"kind": {{Label: draftState(e)}}}}
		_, l.busy = m.busy[taskUnit(e.ID)]
		if since := now.Sub(e.Updated); !e.Updated.IsZero() && since > 0 {
			l.age, l.ageStyle = lens.Age(words, since), m.t.Dim
			if since >= 48*time.Hour {
				l.ageStyle = m.t.Yellow
			}
		}
		lines[i] = l
	}
	return m.lineCells(lines, make([]string, len(tasks)), cur, width, height)
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
	fact("to", draftTarget(draftTask(api.ViewsCatalog{}, *d, "")), plain)
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
