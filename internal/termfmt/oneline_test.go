package termfmt

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestOneLine(t *testing.T) {
	for in, want := range map[string]string{
		"triage asks:\n  keep\tboth?\x1b[2J": "triage asks: keep both? [2J",
		"\u009bx  y\u007f":                   "x y",
		"":                                   "",
		"a\u202eevil\u202c b\u200b\ufeff":    "aevil b",
		"keep \u2067it\u2069 ltr":            "keep it ltr",
	} {
		if got := OneLine(in, 1000); got != want {
			t.Errorf("%q: %q, want %q", in, got, want)
		}
	}
	if got := OneLine(strings.Repeat("é", 1200), 1000); utf8.RuneCountInString(got) != 1000 {
		t.Errorf("cut to %d runes", utf8.RuneCountInString(got))
	}
	if got := OneLine("ab cd", 3); got != "ab" {
		t.Errorf("a cut keeps a trailing space: %q", got)
	}
}
