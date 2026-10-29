package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"strings"
	"time"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/api/client"
	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/readmodel/prrow"
	"github.com/wloophq/wloop/internal/termfmt"
)

// runStatus lists the tasks of the views the badge counts, and the tracked
// pull requests.
func runStatus(ctx context.Context, e *Env, args []string) error {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	fs.SetOutput(e.Err)
	var color termfmt.Mode
	fs.Var(&color, "color", "lay the list out for a terminal: auto, always or never")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return errors.New("usage: " + brand.Command + " status [--color auto|always|never]")
	}
	c, err := e.daemonClient(ctx)
	if err != nil {
		return err
	}
	if ps, err := c.Plugins(ctx); err == nil {
		if off := disabledPlugins(ps); len(off) > 0 {
			fmt.Fprintln(e.Err, "disabled plugins: "+strings.Join(off, ", "))
		}
	}
	return daemonStatus(ctx, e, c, e.term(color))
}

const sectionTracked = "tracked"

// statusSection is one view's lines; reasons says whether a terminal shows
// their reasons.
type statusSection struct {
	name, heading string
	lines         []statusLine
	reasons       bool
}

// statusLine is a task or a pull request as status lists it.
type statusLine struct {
	who, label, link, title, reason string
	since                           time.Time
	stalled                         bool
}

func taskLine(t api.TaskView) statusLine {
	l := statusLine{who: t.OwedBy, title: t.Title, since: t.Updated}
	if len(t.UnitLabels) > 0 {
		l.label = t.UnitLabels[0]
	}
	return l
}

func rowLine(r api.Row) statusLine {
	return statusLine{who: r.Summary.WaitsOn, label: r.Label, link: r.Link, title: r.Title, reason: r.Summary.Reason,
		since: r.Summary.Since, stalled: r.Summary.Stalled}
}

// daemonStatus lists the tasks of the views the badge counts, in the
// profile's order, then the tracked pull requests.
func daemonStatus(ctx context.Context, e *Env, c *client.Client, t termfmt.Term) error {
	cat, err := c.Views(ctx)
	if err != nil {
		return err
	}
	var sections []statusSection
	var labels []string
	for _, v := range cat.Views {
		if !v.Badge {
			continue
		}
		labels = append(labels, v.Label)
		page, err := c.TasksSince(ctx, client.TaskQuery{View: v.ID}, api.Since{})
		if err != nil {
			return err
		}
		if len(page.Tasks) == 0 {
			continue
		}
		sec := statusSection{name: v.ID, heading: v.Label, reasons: true}
		for _, tk := range page.Tasks {
			sec.lines = append(sec.lines, taskLine(tk))
		}
		sections = append(sections, sec)
	}
	rows, err := c.Rows(ctx)
	if err != nil {
		return err
	}
	empty := len(sections) == 0
	tracked := statusSection{name: sectionTracked, heading: sectionTracked}
	for _, r := range rows {
		if p, ok := prrow.PayloadOf(r); ok && p.Override.Tracked {
			tracked.lines = append(tracked.lines, rowLine(r))
		}
	}
	if len(tracked.lines) > 0 {
		sections = append(sections, tracked)
	}
	emptyStatus := "nothing in " + strings.Join(labels, ", ") + "; " + brand.Command + " open shows all"
	now := e.Now()
	switch {
	case !t.TTY:
		writeStatusTSV(e, sections, now)
		if empty {
			fmt.Fprintln(e.Err, emptyStatus)
		}
		return nil
	case empty:
		fmt.Fprintln(e.Out, emptyStatus)
	}
	writeStatus(e, t, sections, now)
	return nil
}

func lineAge(l statusLine, now time.Time) string {
	return shortAge(now.Sub(l.since), l.since.IsZero())
}

// writeStatusTSV writes a line per task or pull request and section, full
// values tab-separated: section, who owes it, age, its link's label, title,
// reason.
func writeStatusTSV(e *Env, sections []statusSection, now time.Time) {
	for _, s := range sections {
		for _, l := range s.lines {
			fields := []string{s.name, l.who, lineAge(l, now), l.label, l.title, l.reason}
			for i, f := range fields {
				fields[i] = oneLine(f)
			}
			fmt.Fprintln(e.Out, strings.Join(fields, "\t"))
		}
	}
}

// writeStatus lays each section out in aligned columns, a reason dimmed
// on the line under its task or pull request.
func writeStatus(e *Env, t termfmt.Term, sections []statusSection, now time.Time) {
	cols := []termfmt.Column{{Right: true}, {}, {Flex: true}, {Max: 24}}
	for i, s := range sections {
		if i > 0 {
			fmt.Fprintln(e.Out)
		}
		fmt.Fprintln(e.Out, t.Bold(strings.ToUpper(s.heading))+" "+t.Dim(fmt.Sprint(len(s.lines))))
		cells := make([][]termfmt.Cell, len(s.lines))
		ageWidth := 0
		for j, l := range s.lines {
			age := lineAge(l, now)
			ageWidth = max(ageWidth, termfmt.Cells(age))
			url := l.link
			label := termfmt.Cell{Text: l.label}
			if url != "" {
				label.Style = func(s string) string { return t.Link(url, s) }
			}
			cells[j] = []termfmt.Cell{{Text: age, Style: lateness(t, l, now)}, label, {Text: l.title}, {Text: l.who}}
		}
		indent := 2 + ageWidth + 2
		for j, line := range t.Columns(t.Width-2, 2, cols, cells) {
			fmt.Fprintln(e.Out, "  "+line)
			if r := s.lines[j].reason; s.reasons && r != "" {
				fmt.Fprintln(e.Out, strings.Repeat(" ", indent)+t.Dim(termfmt.Cut(oneLine(r), t.Width-indent)))
			}
		}
	}
}

// lateness colors an age: red once stalled, yellow from two days, dim
// before.
func lateness(t termfmt.Term, l statusLine, now time.Time) func(string) string {
	switch {
	case l.stalled:
		return t.Red
	case !l.since.IsZero() && now.Sub(l.since) >= 48*time.Hour:
		return t.Yellow
	}
	return t.Dim
}

func shortAge(d time.Duration, unknown bool) string {
	switch {
	case unknown:
		return "-"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(max(d, 0).Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
}

// oneLine is s cleaned with its whitespace runs collapsed to one space.
func oneLine(s string) string { return strings.Join(strings.Fields(termfmt.Clean(s)), " ") }
