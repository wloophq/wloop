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

// runStatus lists the daemon's pull requests by view.
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

var statusViews = []string{api.ViewNeedsMe, api.ViewStalled, api.ViewMine, api.ViewLoop}

const sectionTracked = "tracked"

// statusSection is one view's rows; reasons says whether its lines carry them.
type statusSection struct {
	name    string
	rows    []api.Row
	reasons bool
}

func daemonStatus(ctx context.Context, e *Env, c *client.Client, t termfmt.Term) error {
	rows, err := c.Rows(ctx)
	if err != nil {
		return err
	}
	byView := map[string][]api.Row{}
	var tracked []api.Row
	for _, r := range rows {
		if p, ok := prrow.PayloadOf(r); ok && p.Override.Tracked {
			tracked = append(tracked, r)
		}
		for _, v := range r.Views {
			byView[v] = append(byView[v], r)
		}
	}
	var sections []statusSection
	for _, v := range statusViews {
		if len(byView[v]) > 0 {
			sections = append(sections, statusSection{v, byView[v], true})
		}
	}
	empty := len(sections) == 0
	if len(tracked) > 0 {
		sections = append(sections, statusSection{sectionTracked, tracked, false})
	}
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

var emptyStatus = "nothing in needs-me, stalled, mine or loop; " + brand.Command + " open shows all"

func rowAge(r api.Row, now time.Time) string {
	return shortAge(now.Sub(r.Summary.Since), r.Summary.Since.IsZero())
}

// writeStatusTSV writes a row per pull request and section, full values
// tab-separated: section, waiting-on, age, owner/repo#n, title, reason.
func writeStatusTSV(e *Env, sections []statusSection, now time.Time) {
	for _, s := range sections {
		for _, r := range s.rows {
			fields := []string{s.name, r.Summary.WaitsOn, rowAge(r, now), r.Label, r.Title, r.Summary.Reason}
			for i, f := range fields {
				fields[i] = oneLine(f)
			}
			fmt.Fprintln(e.Out, strings.Join(fields, "\t"))
		}
	}
}

// writeStatus lays each section out in aligned columns, the reason dimmed
// on the line under the pull request.
func writeStatus(e *Env, t termfmt.Term, sections []statusSection, now time.Time) {
	cols := []termfmt.Column{{Right: true}, {}, {Flex: true}, {Max: 24}}
	for i, s := range sections {
		if i > 0 {
			fmt.Fprintln(e.Out)
		}
		fmt.Fprintln(e.Out, t.Bold(strings.ToUpper(strings.ReplaceAll(s.name, "-", " ")))+" "+t.Dim(fmt.Sprint(len(s.rows))))
		cells := make([][]termfmt.Cell, len(s.rows))
		ageWidth := 0
		for j, r := range s.rows {
			age := rowAge(r, now)
			ageWidth = max(ageWidth, termfmt.Cells(age))
			url := r.Link
			cells[j] = []termfmt.Cell{
				{Text: age, Style: lateness(t, r, now)},
				{Text: r.Label, Style: func(s string) string { return t.Link(url, s) }},
				{Text: r.Title},
				{Text: r.Summary.WaitsOn},
			}
		}
		indent := 2 + ageWidth + 2
		for j, line := range t.Columns(t.Width-2, 2, cols, cells) {
			fmt.Fprintln(e.Out, "  "+line)
			if r := s.rows[j]; s.reasons && r.Summary.Reason != "" {
				fmt.Fprintln(e.Out, strings.Repeat(" ", indent)+t.Dim(termfmt.Cut(oneLine(r.Summary.Reason), t.Width-indent)))
			}
		}
	}
}

// lateness colors an age: red once the pull request is stalled, yellow
// from two days, dim before.
func lateness(t termfmt.Term, r api.Row, now time.Time) func(string) string {
	switch {
	case r.Summary.Stalled:
		return t.Red
	case !r.Summary.Since.IsZero() && now.Sub(r.Summary.Since) >= 48*time.Hour:
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
