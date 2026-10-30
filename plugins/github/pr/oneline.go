package pr

import (
	"strings"
	"unicode"
)

// OneLine is s on one line: control and invisible characters dropped or
// blanked, whitespace collapsed, cut to max runes.
func OneLine(s string, max int) string {
	s = strings.Map(func(r rune) rune {
		switch {
		case r < 0x20 || r == 0x7f || (r >= 0x80 && r < 0xa0):
			return ' '
		case unicode.Is(unicode.Bidi_Control, r), r == 0x200b, r == 0x200c, r == 0x2060, r == 0xfeff:
			return -1
		}
		return r
	}, s)
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > max {
		s = strings.TrimRight(string(r[:max]), " ")
	}
	return s
}
