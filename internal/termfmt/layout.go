package termfmt

import (
	"strings"

	"github.com/clipperhouse/uax29/v2/graphemes"
)

// Cell is one value of a row and the style it is printed in.
type Cell struct {
	Text  string
	Style func(string) string
}

// Column lays out one column: Right aligns it to the right, Max caps its
// width, and the Flex column takes what the others leave of the width.
type Column struct {
	Right, Flex bool
	Max         int
}

// Columns lays rows out gap spaces apart within width cells: each column as
// wide as its widest value, the flexible one cut to the room left. Cells are
// measured and cut plain, then styled.
func (t Term) Columns(width, gap int, cols []Column, rows [][]Cell) []string {
	w := make([]int, len(cols))
	for _, r := range rows {
		for i := range cols {
			if i < len(r) {
				w[i] = max(w[i], Cells(Clean(r[i].Text)))
			}
		}
	}
	flex, used := -1, gap*(len(cols)-1)
	for i, c := range cols {
		if c.Max > 0 {
			w[i] = min(w[i], c.Max)
		}
		if c.Flex {
			flex = i
			continue
		}
		used += w[i]
	}
	if flex >= 0 {
		w[flex] = max(0, min(w[flex], width-used))
	}
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		var b strings.Builder
		for i, c := range cols {
			var cell Cell
			if i < len(r) {
				cell = r[i]
			}
			text := Cut(cell.Text, w[i])
			pad := strings.Repeat(" ", w[i]-Cells(text))
			if cell.Style != nil && text != "" {
				text = cell.Style(text)
			}
			switch {
			case c.Right:
				b.WriteString(pad + text)
			case i < len(cols)-1:
				b.WriteString(text + pad)
			default:
				b.WriteString(text)
			}
			if i < len(cols)-1 {
				b.WriteString(strings.Repeat(" ", gap))
			}
		}
		out = append(out, strings.TrimRight(b.String(), " "))
	}
	return out
}

// Wrap breaks s at spaces into lines of at most first cells, then rest
// cells, for a caller that hangs the later lines under the first; a word
// wider than a line is cut across lines.
func Wrap(s string, first, rest int) []string {
	var lines []string
	line, room := "", max(first, 1)
	flush := func() {
		lines = append(lines, line)
		line, room = "", max(rest, 1)
	}
	for _, word := range strings.Fields(Clean(s)) {
		if line != "" && Cells(line)+1+Cells(word) <= room {
			line += " " + word
			continue
		}
		if line != "" {
			flush()
		}
		for Cells(word) > room {
			head := Prefix(word, room)
			if head == "" {
				g := graphemes.FromString(word)
				g.Next()
				head = g.Value()
			}
			line, word = head, word[len(head):]
			flush()
		}
		line = word
	}
	if line != "" {
		lines = append(lines, line)
	}
	return lines
}
