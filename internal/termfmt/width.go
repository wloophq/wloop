package termfmt

import (
	"strings"
	"unicode"

	"github.com/clipperhouse/uax29/v2/graphemes"
	"github.com/mattn/go-runewidth"
)

// Clean keeps text to what a terminal prints in place. Control characters
// become spaces. Bidi controls, line and paragraph separators and invisible
// format characters, which a title could use to reorder or fake the line,
// are removed; the zero-width joiner and non-joiner stay, as emoji and
// scripts such as Persian need them.
func Clean(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r < 0x20 || r == 0x7f || (r >= 0x80 && r < 0xa0):
			return ' '
		case Joiner(r):
			return r
		case unicode.In(r, unicode.Cf, unicode.Zl, unicode.Zp):
			return -1
		}
		return r
	}, s)
}

// Joiner says whether r is the zero-width joiner or non-joiner.
func Joiner(r rune) bool { return r == 0x200c || r == 0x200d }

// widths follows the locale, so East Asian ambiguous characters take the two
// cells a CJK terminal gives them.
var widths = runewidth.NewCondition()

// UseWidths measures with c until the returned function restores the
// locale's measure; tests use it to measure as a CJK terminal does.
func UseWidths(c *runewidth.Condition) (restore func()) {
	saved := widths
	widths = c
	return func() { widths = saved }
}

const Ellipsis = "…"

// clusterWidth is the cells of one grapheme cluster. A narrow symbol asked
// for as emoji (♻️, ⬆️: U+FE0F after it) takes two cells in the terminals
// that draw emoji, which go-runewidth does not count.
func clusterWidth(c string) int {
	w := widths.StringWidth(c)
	if w == 1 && strings.ContainsRune(c, 0xfe0f) {
		return 2
	}
	return w
}

// Cells is the width of s on a terminal.
func Cells(s string) int {
	n := 0
	for g := graphemes.FromString(s); g.Next(); {
		n += clusterWidth(g.Value())
	}
	return n
}

// Prefix is the longest start of s, in whole clusters, within w cells.
func Prefix(s string, w int) string {
	used := 0
	for g := graphemes.FromString(s); g.Next(); {
		cw := clusterWidth(g.Value())
		if used+cw > w {
			return s[:g.Start()]
		}
		used += cw
	}
	return s
}

// Suffix is the longest end of s, in whole clusters, within w cells.
func Suffix(s string, w int) string {
	var starts, cw []int
	for g := graphemes.FromString(s); g.Next(); {
		starts = append(starts, g.Start())
		cw = append(cw, clusterWidth(g.Value()))
	}
	start, used := len(s), 0
	for i := len(starts) - 1; i >= 0 && used+cw[i] <= w; i-- {
		start, used = starts[i], used+cw[i]
	}
	return s[start:]
}

// Cut cleans s and cuts it to w cells, marking a cut with an ellipsis.
func Cut(s string, w int) string {
	if w <= 0 {
		return ""
	}
	s = Clean(s)
	if Cells(s) > w {
		tail := Ellipsis
		if Cells(tail) > w {
			tail = ""
		}
		s = Prefix(s, w-Cells(tail)) + tail
	}
	return s
}

// Fit cuts s to w cells and pads it to w.
func Fit(s string, w int) string {
	s = Cut(s, w)
	return s + strings.Repeat(" ", max(w-Cells(s), 0))
}

// FitRight is Fit aligned to the right.
func FitRight(s string, w int) string {
	s = Cut(s, w)
	return strings.Repeat(" ", max(w-Cells(s), 0)) + s
}
