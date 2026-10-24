package cli

import (
	"fmt"
	"io"
	"os"
	"regexp"
	"slices"
	"strings"

	"github.com/wloophq/wloop/internal/termfmt"
)

type checkSection struct {
	title  string
	checks []check
}

const homesSection = "Agent homes"

// doctorWidth caps the report on a wide terminal, so a header's counts stay
// near its title.
const doctorWidth = 110

// term decides how to write to e.Out.
func (e *Env) term(mode termfmt.Mode) termfmt.Term { return e.termOf(e.Out, mode) }

func (e *Env) termOf(w io.Writer, mode termfmt.Mode) termfmt.Term {
	getenv := e.Getenv
	if getenv == nil {
		getenv = os.Getenv
	}
	return termfmt.Detect(w, getenv, mode)
}

// dedupe keeps the first check of each name and drops empty sections.
func dedupe(sections []checkSection) []checkSection {
	listed := map[string]bool{}
	var out []checkSection
	for _, s := range sections {
		var kept []check
		for _, c := range s.checks {
			if !listed[c.name] {
				listed[c.name] = true
				kept = append(kept, c)
			}
		}
		if len(kept) > 0 {
			out = append(out, checkSection{s.title, kept})
		}
	}
	return out
}

func pending(sections []checkSection) int {
	n := 0
	for _, s := range sections {
		for _, c := range s.checks {
			if c.mark == markMissing {
				n++
			}
		}
	}
	return n
}

// writeDoctorPlain is the report scripts and agents read, one line a check.
func writeDoctorPlain(w io.Writer, sections []checkSection) {
	for _, s := range sections {
		for _, c := range s.checks {
			line := fmt.Sprintf("  [%s] %s", c.mark, c.name)
			if c.info != "" {
				line += ": " + c.info
			}
			fmt.Fprintln(w, line)
			if c.mark == markMissing && c.fix != "" {
				fmt.Fprintf(w, "            fix with: %s\n", c.fix)
			}
		}
	}
	fmt.Fprintln(w)
}

// doctorView writes the report for a person: sections with their counts,
// quiet ok lines, problems loud with the fix on its own line.
type doctorView struct {
	t     termfmt.Term
	w     io.Writer
	width int
	home  string
	// homeRe finds the home directory at the start of a path.
	homeRe *regexp.Regexp
}

func (e *Env) doctorView(t termfmt.Term) doctorView {
	v := doctorView{t: t, w: e.Out, width: reportWidth(t), home: e.Home}
	if e.Home != "" {
		v.homeRe = regexp.MustCompile(`(^|[\s"'=(])` + regexp.QuoteMeta(e.Home) + `(/|$|[^\w.-])`)
	}
	return v
}

func (e *Env) writeDoctor(t termfmt.Term, sections []checkSection) {
	v := e.doctorView(t)
	for i, s := range sections {
		if i > 0 {
			fmt.Fprintln(v.w)
		}
		v.header(s)
		if s.title == homesSection {
			v.homes(s.checks)
			continue
		}
		nw := nameWidth(s.checks, func(c check) string { return c.name })
		for _, c := range s.checks {
			v.check(2, nw, c.name, c)
		}
	}
	fmt.Fprintln(v.w)
}

func (v doctorView) header(s checkSection) {
	counts := map[mark]int{}
	for _, c := range s.checks {
		counts[c.mark]++
	}
	var labels []tally
	for _, m := range []struct {
		mark  mark
		label string
		style func(string) string
	}{{markOK, "ok", v.t.Dim}, {markInfo, "info", v.t.Dim}, {markMissing, "missing", v.t.Red}} {
		if n := counts[m.mark]; n > 0 {
			labels = append(labels, tally{fmt.Sprintf("%d %s", n, m.label), m.style})
		}
	}
	v.title(s.title, labels)
}

// tally is one label of a section header's summary and its style.
type tally struct {
	text  string
	style func(string) string
}

// title writes a section's title bold with its counts at the right edge.
func (v doctorView) title(title string, counts []tally) {
	plain, styled := make([]string, len(counts)), make([]string, len(counts))
	for i, c := range counts {
		plain[i], styled[i] = c.text, c.style(c.text)
	}
	summary := strings.Join(plain, " · ")
	if summary == "" {
		fmt.Fprintln(v.w, v.t.Bold(title))
		return
	}
	gap := v.width - termfmt.Cells(title) - termfmt.Cells(summary)
	if gap < 2 {
		fmt.Fprintln(v.w, v.t.Bold(title))
		line, used := "", 0
		for i, c := range counts {
			n := termfmt.Cells(c.text)
			if used > 0 && 2+used+3+n > v.width {
				fmt.Fprintln(v.w, "  "+line)
				line, used = "", 0
			}
			if used > 0 {
				line, used = line+v.t.Dim(" · "), used+3
			}
			line, used = line+styled[i], used+n
		}
		fmt.Fprintln(v.w, "  "+line)
		return
	}
	fmt.Fprintln(v.w, v.t.Bold(title)+strings.Repeat(" ", gap)+strings.Join(styled, v.t.Dim(" · ")))
}

// nameWidth is the name column of a list of checks, capped so a long name
// sends its info to the next line instead of pushing every row right.
func nameWidth(checks []check, name func(check) string) int {
	w := 0
	for _, c := range checks {
		if n := termfmt.Cells(name(c)); n <= 32 {
			w = max(w, n)
		}
	}
	return w
}

func (v doctorView) glyph(c check) string {
	switch {
	case c.mark == markOK:
		return v.t.Green("✓")
	case c.mark == markInfo:
		return v.t.Cyan("i")
	case c.fix == "":
		return v.t.Yellow("!")
	}
	return v.t.Red("✗")
}

// check writes one check at indent: the glyph, the name in a column nw
// wide, the info wrapped under itself and the fix on a line of its own.
func (v doctorView) check(indent, nw int, name string, c check) {
	pad := strings.Repeat(" ", indent)
	name = termfmt.Clean(name)
	head := pad + v.glyph(c) + " " + name
	col := indent + 2 + nw + 2
	room := v.width - col
	info := v.t.Dim
	if c.mark == markMissing {
		info = func(s string) string { return s }
	}
	var lines []string
	for _, para := range strings.Split(v.tildeText(c.info), "\n") {
		lines = append(lines, termfmt.Wrap(strings.TrimSpace(para), room, room)...)
	}
	switch n := termfmt.Cells(name); {
	case len(lines) == 0:
		fmt.Fprintln(v.w, head)
	case n > nw:
		fmt.Fprintln(v.w, head)
		v.lines(col, lines, info)
	default:
		fmt.Fprintln(v.w, head+strings.Repeat(" ", nw-n+2)+info(lines[0]))
		v.lines(col, lines[1:], info)
	}
	if c.mark == markMissing && c.fix != "" {
		fmt.Fprintln(v.w, pad+"    "+v.t.Yellow("fix:")+" "+termfmt.Clean(c.fix))
	}
}

func (v doctorView) lines(indent int, lines []string, style func(string) string) {
	for _, l := range lines {
		fmt.Fprintln(v.w, strings.Repeat(" ", indent)+style(l))
	}
}

// homeEntry is a check of the agent homes split into its home and entry.
type homeEntry struct {
	home, entry string
	check
}

func splitHome(c check) (homeEntry, bool) {
	if rest, ok := strings.CutPrefix(c.name, "agent home "); ok {
		home, entry, found := strings.Cut(rest, ": ")
		if !found {
			entry = "home"
		}
		return homeEntry{home, entry, c}, true
	}
	if dir, ok := strings.CutPrefix(c.name, "skills in "); ok {
		return homeEntry{dir, "skills", c}, true
	}
	return homeEntry{}, false
}

// homes groups the agent homes' checks by home: one line of the entries in
// order, then each entry with something to say.
func (v doctorView) homes(checks []check) {
	var order []string
	byHome := map[string][]homeEntry{}
	var loose []check
	for _, c := range checks {
		h, ok := splitHome(c)
		if !ok {
			loose = append(loose, c)
			continue
		}
		if _, seen := byHome[h.home]; !seen {
			order = append(order, h.home)
		}
		byHome[h.home] = append(byHome[h.home], h)
	}
	var said []check
	for _, home := range order {
		for _, h := range byHome[home] {
			if h.mark != markOK || h.info != "" {
				said = append(said, check{h.mark, h.entry, h.info, h.fix})
			}
		}
	}
	nw := nameWidth(append(slices.Clone(loose), said...), func(c check) string { return c.name })
	for _, c := range loose {
		v.check(2, nw, c.name, c)
	}
	for _, home := range order {
		fmt.Fprintln(v.w, "  "+v.t.Bold(v.tilde(home)))
		var quiet []string
		for _, h := range byHome[home] {
			if h.mark == markOK && h.info == "" {
				quiet = append(quiet, h.entry)
			}
		}
		v.quiet(4, quiet)
		for _, h := range byHome[home] {
			if h.mark != markOK || h.info != "" {
				v.check(4, nw, h.entry, h.check)
			}
		}
	}
}

// quiet writes ok entries side by side, wrapped at the width.
func (v doctorView) quiet(indent int, entries []string) {
	pad := strings.Repeat(" ", indent)
	line, used := "", indent
	for _, e := range entries {
		e = termfmt.Clean(e)
		n := 2 + termfmt.Cells(e)
		if line != "" && used+2+n > v.width {
			fmt.Fprintln(v.w, pad+line)
			line, used = "", indent
		}
		if line != "" {
			line, used = line+"  ", used+2
		}
		line, used = line+v.t.Green("✓")+" "+e, used+n
	}
	if line != "" {
		fmt.Fprintln(v.w, pad+line)
	}
}

// tildeText writes the home directory as ~ where a path starts with it.
func (v doctorView) tildeText(s string) string {
	if v.homeRe == nil {
		return s
	}
	return v.homeRe.ReplaceAllString(s, "${1}~${2}")
}

func (v doctorView) tilde(path string) string { return tildePath(v.home, path) }

// tildePath writes path with home as ~, for prose; a command keeps the path.
func tildePath(home, path string) string {
	if home != "" && (path == home || strings.HasPrefix(path, home+"/")) {
		return "~" + path[len(home):]
	}
	return path
}
