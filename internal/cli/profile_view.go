package cli

import (
	"fmt"
	"path/filepath"
	"strings"

	dintent "github.com/wloophq/wloop/internal/domain/intent"
	intentload "github.com/wloophq/wloop/internal/intent"
	"github.com/wloophq/wloop/internal/profile"
	"github.com/wloophq/wloop/internal/termfmt"
)

// reportWidth is the width a sectioned report wraps at.
func reportWidth(t termfmt.Term) int { return min(max(t.Width, 40), doctorWidth) }

func (e *Env) sectionHeader(t termfmt.Term, title string, n int) {
	fmt.Fprintln(e.Out, t.Bold(title)+" "+t.Dim(fmt.Sprint(n)))
}

// minRoom is the narrowest a wrapped column gets; with less room left of
// its column, the text moves under the line at shortIndent.
const (
	minRoom     = 20
	shortIndent = 6
	// columnCap caps a path column; a longer path takes its line alone.
	columnCap = 48
)

// textColumn is where text wrapped from col starts at width.
func textColumn(width, col int) int {
	if width-col < minRoom {
		return min(col, shortIndent)
	}
	return col
}

// columned is head, then text wrapped from col, its first line beside head
// when head leaves room, else every line under it.
func columned(width int, head string, col int, text string, style func(string) string) []string {
	col = textColumn(width, col)
	lines := termfmt.Wrap(text, width-col, width-col)
	hw := termfmt.Cells(termfmt.Plain(head))
	var out []string
	if len(lines) > 0 && hw+2 <= col {
		out = append(out, head+strings.Repeat(" ", col-hw)+style(lines[0]))
		lines = lines[1:]
	} else {
		out = append(out, head)
	}
	for _, l := range lines {
		out = append(out, strings.Repeat(" ", col)+style(l))
	}
	return out
}

// hanging is head, then text wrapped with its later lines under its first.
func hanging(width int, head, text string, style func(string) string) []string {
	col := termfmt.Cells(termfmt.Plain(head))
	if textColumn(width, col) == col {
		lines := termfmt.Wrap(text, width-col, width-col)
		if len(lines) == 0 {
			return []string{strings.TrimRight(head, " ")}
		}
		out := []string{head + style(lines[0])}
		for _, l := range lines[1:] {
			out = append(out, strings.Repeat(" ", col)+style(l))
		}
		return out
	}
	return columned(width, strings.TrimRight(head, " "), col, text, style)
}

func plain(s string) string { return s }

// pathHead is lead then path, lead being indent wide: head, for a column to
// follow, when it fits the width; else lead alone and the path wrapped under
// it at indent, all above an empty head.
func pathHead(width int, lead string, indent int, path string, style func(string) string) (above []string, head string) {
	if indent+termfmt.Cells(path) <= width {
		return nil, lead + style(path)
	}
	if l := strings.TrimRight(lead, " "); termfmt.Plain(l) != strings.Repeat(" ", termfmt.Cells(termfmt.Plain(l))) {
		above = append(above, l)
	}
	return append(above, hanging(width, strings.Repeat(" ", indent), path, style)...), ""
}

// pathColumn is the width of a path column that starts at before: the
// widest path within columnCap and within what leaves minRoom to the text
// after it. A wider path takes its line alone.
func pathColumn(width, before int, widths ...int) int {
	limit := min(columnCap, width-before-2-minRoom)
	w := 0
	for _, n := range widths {
		if n <= limit {
			w = max(w, n)
		}
	}
	return w
}

// writeIntents lists the intents in columns: name, where it is declared
// (relative to the profile) and its summary wrapped under itself.
func (e *Env) writeIntents(t termfmt.Term, dir string, intents *dintent.Registry) {
	width := reportWidth(t)
	defs := intents.All()
	e.sectionHeader(t, "Intents", len(defs))
	names, sources := make([]string, len(defs)), make([]string, len(defs))
	nw := 0
	var sws []int
	for i, d := range defs {
		names[i], sources[i] = termfmt.Clean(string(d.Name)), "bundled"
		if !d.Bundled {
			sources[i] = termfmt.Clean(e.inProfile(dir, filepath.Join(d.Source.Dir, "intent.toml")))
		}
		nw, sws = max(nw, termfmt.Cells(names[i])), append(sws, termfmt.Cells(sources[i]))
	}
	sw := pathColumn(width, 2+nw+2, sws...)
	for i, d := range defs {
		above, head := pathHead(width, "  "+names[i]+strings.Repeat(" ", nw-termfmt.Cells(names[i])+2), 2+nw+2, sources[i], t.Dim)
		for _, l := range append(above, columned(width, head, 2+nw+2+sw+2, d.Summary, plain)...) {
			fmt.Fprintln(e.Out, l)
		}
	}
	fmt.Fprintln(e.Out)
}

// inProfile names path relative to the profile dir, or with ~ outside it.
func (e *Env) inProfile(dir, path string) string {
	if rel, err := filepath.Rel(dir, path); err == nil && !strings.HasPrefix(rel, "..") {
		return filepath.ToSlash(rel)
	}
	return tildePath(e.Home, path)
}

// writeBlocks lists the profile's blocks per intent in aligned columns:
// intent, path and its conditions wrapped under themselves.
func (e *Env) writeBlocks(t termfmt.Term, dir string, tree profile.Tree, intents *dintent.Registry) {
	type row struct{ label, path, conds string }
	var rows []row
	lw := 0
	var pws []int
	for _, k := range profile.Kinds(intents) {
		label := termfmt.Clean(string(k))
		for _, b := range tree.Blocks {
			if b.Path.Intent != k || b.Layer.Dir == "" {
				continue
			}
			r := row{label, termfmt.Clean(b.Rel()), profile.Conditions(b.Path, b.FM.Paths)}
			rows = append(rows, r)
			lw, pws = max(lw, termfmt.Cells(r.label)), append(pws, termfmt.Cells(r.path))
			label = ""
		}
	}
	e.sectionHeader(t, "Blocks", len(rows))
	width := reportWidth(t)
	col := 2 + lw + 2 + pathColumn(width, 2+lw+2, pws...) + 2
	for _, r := range rows {
		label := r.label
		if label != "" {
			label = t.Bold(label)
		}
		above, head := pathHead(width, "  "+label+strings.Repeat(" ", lw-termfmt.Cells(r.label)+2), 2+lw+2, r.path, plain)
		for _, l := range append(above, columned(width, head, col, r.conds, t.Dim)...) {
			fmt.Fprintln(e.Out, l)
		}
	}
	fmt.Fprintln(e.Out)
	const ok = "no errors"
	lines := hanging(width, "", fmt.Sprintf("%s: %d block(s), %s", tildePath(e.Home, dir), len(tree.Blocks), ok), plain)
	if last := lines[len(lines)-1]; strings.HasSuffix(last, ok) {
		lines[len(lines)-1] = strings.TrimSuffix(last, ok) + t.Green(ok)
	}
	for _, l := range lines {
		fmt.Fprintln(e.Out, l)
	}
}

// writeProblems lists the manifests' problems on e.Err: the location red,
// the expression and its caret dimmed under it.
func (e *Env) writeProblems(t termfmt.Term, problems intentload.Problems) {
	fmt.Fprintln(e.Err, t.Bold("Problems")+" "+t.Dim(fmt.Sprint(len(problems))))
	for _, p := range problems {
		loc := p.File
		if p.Line > 0 {
			loc = fmt.Sprintf("%s:%d:%d", p.File, p.Line, p.Col)
		}
		var rest []string
		for _, s := range []string{p.Field, p.Msg} {
			if s != "" {
				rest = append(rest, termfmt.Clean(s))
			}
		}
		fmt.Fprintln(e.Err, "  "+t.Red("✗")+" "+t.Red(loc)+": "+strings.Join(rest, ": "))
		if p.Snippet != "" {
			for _, l := range strings.Split(p.Snippet, "\n") {
				fmt.Fprintln(e.Err, "      "+t.Dim(termfmt.Clean(l)))
			}
		}
	}
	fmt.Fprintln(e.Err)
}
