package tui

import (
	"strings"
	"time"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/api/lens"
	"github.com/wloophq/wloop/internal/client/texts"
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
		l := listLine{label: draftTarget(m.w(), e), title: text, changed: e.Changed, parts: map[string][]lens.Piece{"kind": {{Label: draftState(m.w(), e)}}}}
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
func draftTarget(w *texts.Catalog, e api.TaskView) string {
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
		raw += " (" + w.T("destination unknown", nil) + ")"
	}
	return raw
}

// draftState is the draft's state and how it is approved.
func draftState(w *texts.Catalog, e api.TaskView) string {
	d := draftOn(e)
	if d == nil {
		return e.State
	}
	parts := []string{d.State}
	if d.Deadline != nil {
		parts = append(parts, w.T("sends {time}", map[string]any{"time": d.Deadline.Local().Format("15:04")}))
	}
	return strings.Join(parts, " · ")
}

// draftFacts adds the draft's target, its whole text, kept in its
// paragraphs, why it failed, and when its window sends it.
func (m *Model) draftFacts(d *api.Draft, fact func(label, text string, style func(string) string), para func(label, text string)) {
	t := m.t
	fact(m.w().T("to", nil), draftTarget(m.w(), draftTask(api.ViewsCatalog{}, *d, "")), plain)
	head := d.Purpose
	if d.Edited {
		head += " · " + m.w().T("edited", nil)
	}
	if d.Origin.Kind != "" {
		head += " · " + m.w().T("from {origin}", map[string]any{"origin": d.Origin.Kind})
	}
	fact(m.w().T("draft", nil), strings.Trim(head, " ·"), t.Dim)
	if d.Preview != "" {
		fact("", d.Preview, plain)
	}
	para(m.w().T("text", nil), d.Text)
	fieldFacts(d.Fields, "", fact)
	if d.Reaction != "" {
		fact(m.w().T("reaction", nil), d.Reaction, plain)
	}
	if d.Error != "" {
		fact(m.w().T("error", nil), d.Error, t.Red)
	}
	if d.Deadline != nil {
		fact(m.w().T("sends", nil), m.w().T("{time} unless you decide first", map[string]any{"time": m.w().Time(d.Deadline.Local(), "datetime")}), t.Dim)
	}
}

// fieldFacts show every value of a draft's payload, each under its field's word, nested fields indented.
func fieldFacts(fs []api.DraftField, indent string, fact func(label, text string, style func(string) string)) {
	for _, f := range fs {
		if len(f.Fields) > 0 {
			fact(indent+f.Label, "", plain)
			fieldFacts(f.Fields, indent+"  ", fact)
			continue
		}
		v := f.Value
		if f.Shown != "" && f.Shown != f.Value {
			v = f.Shown + " (" + f.Value + ")"
		}
		fact(indent+f.Label, v, plain)
	}
}
