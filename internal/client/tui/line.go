package tui

import (
	"slices"
	"strings"
	"time"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/api/lens"
	"github.com/wloophq/wloop/internal/termfmt"
)

// listLine is one line of a list, a row's or a task's: its parts as the lens places them and what no facet
// carries.
type listLine struct {
	parts map[string][]lens.Piece
	// label is the unit's own label, shown after the ref part's pieces it does not start with.
	label, link, title string
	// facts follow the meta part, as a project's review does.
	facts []string
	// age overrides the age part when set, as a round's countdown does.
	age      string
	ageStyle func(string) string
	// busy and changed mark the line beside the cursor.
	busy, changed bool
}

// lineCells lays lines out in aligned columns: the age, the ref, the title, from 100 columns the meta part
// and the facts, and from 60 whom it waits on with its kind; heads open the groups, and the height of them
// around the cursor shows.
func (m *Model) lineCells(lines []listLine, heads []string, cur, width, height int) []string {
	t := m.t
	cols := []termfmt.Column{{Right: true}, {}, {Flex: true}}
	meta := width >= 100
	if meta {
		cols = append(cols, termfmt.Column{Max: 36})
	}
	whoMax := 14
	switch {
	case width >= 120:
		whoMax = 36
	case width >= 100:
		whoMax = 28
	case width >= 80:
		whoMax = 20
	}
	refMax := min(max((width-2)/3, 14), 40)
	if width >= 60 {
		cols = append(cols, termfmt.Column{Max: whoMax})
	} else {
		refMax = (width - 9) / 2
	}
	cells := make([][]termfmt.Cell, len(lines))
	for i, l := range lines {
		age, style := l.age, l.ageStyle
		if age == "" {
			age, style = labels(l.parts["age"], " "), t.Dim
		}
		url := l.link
		c := []termfmt.Cell{
			{Text: cmpOr(age, "-"), Style: style},
			{Text: shortRef(refText(l), refMax), Style: func(s string) string { return t.Link(url, s) }},
			{Text: oneLine(l.title)},
		}
		if meta {
			c = append(c, termfmt.Cell{Text: oneLine(strings.Join(append([]string{labels(l.parts["meta"], " ")}, l.facts...), " ")), Style: t.Dim})
		}
		if width >= 60 {
			c = append(c, termfmt.Cell{Text: whoCell(l, whoMax)})
		}
		cells[i] = c
	}
	laid := t.Columns(width-3, 2, cols, cells)
	var out []string
	var at []int
	for i, l := range lines {
		if heads[i] != "" {
			out, at = append(out, " "+t.Bold(termfmt.Cut(oneLine(heads[i]), width-1))), append(at, -1)
		}
		mark := []rune("   ")
		if i == cur {
			mark[0] = '>'
		}
		switch {
		case l.busy:
			mark[1] = '*'
		case l.changed:
			mark[1] = '•'
		}
		line := string(mark) + laid[i]
		if i == cur && t.Color {
			line = t.Reverse(line + strings.Repeat(" ", max(width-termfmt.Cells(termfmt.Plain(line)), 0)))
		}
		out, at = append(out, line), append(at, i)
	}
	return window(out, slices.Index(at, cur), height)
}

func cmpOr(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

func labels(ps []lens.Piece, sep string) string {
	out := make([]string, len(ps))
	for i, p := range ps {
		out[i] = p.Label
	}
	return strings.Join(out, sep)
}

// refText is the ref part's pieces, then the unit's label past the first piece when it starts with it.
func refText(l listLine) string {
	ref := labels(l.parts["ref"], " ")
	tail := l.label
	if ps := l.parts["ref"]; len(ps) > 0 && strings.HasPrefix(tail, ps[0].Label) {
		tail = tail[len(ps[0].Label):]
	}
	return termfmt.Clean(ref + tail)
}

// whoCell is the who part and the kind part within w cells, the names cut before the kind.
func whoCell(l listLine, w int) string {
	who, kind := oneLine(labels(l.parts["who"], ", ")), oneLine(labels(l.parts["kind"], ", "))
	if kind == "" || who == "" {
		return termfmt.Cut(who+kind, w)
	}
	if room := w - termfmt.Cells(kind) - 3; termfmt.Cells(who) > room && room >= 2 {
		who = termfmt.Cut(who, room)
	}
	return who + " · " + kind
}

// rowLine is a row's line: its facets placed by the catalog's specs, a pending round's countdown in place
// of its age, and its age marked ! and red once stalled, yellow from two days.
func (m *Model) rowLine(r api.Row, now time.Time) listLine {
	t := m.t
	words := m.catalog.RowWordsOf(string(r.Unit.Provider), m.viewsCat.Lens)
	l := listLine{parts: lens.Parts(m.rowItem(r), m.catalog.Facets, words, now), label: r.Label, link: r.Link, title: r.Title}
	if r.Stale {
		l.title = staleMark + " " + r.Title
	}
	_, l.busy = m.busy[r.Unit]
	if g, pending := m.roundGate(r); pending {
		l.age, l.ageStyle = countdownText(g, now), t.Cyan
		return l
	}
	ages := l.parts["age"]
	switch {
	case len(ages) == 0:
	case r.Summary.Stalled:
		l.age, l.ageStyle = "!"+ages[0].Label, t.Red
	case olderThan(ages[0], now, 48*time.Hour):
		l.age, l.ageStyle = ages[0].Label, t.Yellow
	}
	return l
}

// taskLine is a task's line: its facets placed by the profile's task specs, its first unit's label, a star
// before the title of a starred task.
func (m *Model) taskLine(e api.TaskView, now time.Time) listLine {
	l := listLine{parts: lens.Parts(taskItem(e), m.viewsCat.Facets, m.viewsCat.Lens, now), title: e.Title, changed: e.Changed,
		facts: projectFacts(e, m.viewsCat.Lens.RowWords)}
	if len(e.Units) > 0 {
		l.label = e.UnitLabel(0)
	}
	if m.viewsCat.Starred(e) {
		l.title = "★ " + l.title
	}
	_, l.busy = m.busy[taskUnit(e.ID)]
	if ages := l.parts["age"]; len(ages) > 0 && olderThan(ages[0], now, 48*time.Hour) {
		l.age, l.ageStyle = ages[0].Label, m.t.Yellow
	}
	return l
}

func olderThan(p lens.Piece, now time.Time, d time.Duration) bool {
	at, err := time.Parse(time.RFC3339, p.Raw)
	return err == nil && now.Sub(at) >= d
}
