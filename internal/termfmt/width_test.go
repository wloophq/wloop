package termfmt

import (
	"strings"
	"testing"
)

func TestKnownWidths(t *testing.T) {
	for s, want := range map[string]int{"✅": 2, "✨": 2, "⚡️": 2, "♻️": 2, "é": 1, "日": 2, "a": 1, "🇧🇷": 2} {
		if got := Cells(s); got != want {
			t.Errorf("%q: %d cells, want %d", s, got, want)
		}
	}
	if got := Cells("👨‍👩‍👧‍👦"); got > 6 || got < 2 {
		t.Errorf("ZWJ family: %d cells", got)
	}
}

func TestFitCutsAndPadsInWholeClusters(t *testing.T) {
	for _, tc := range []struct {
		s    string
		w    int
		want string
	}{
		{"abc", 5, "abc  "},
		{"abcdef", 4, "abc…"},
		{"日本語", 4, "日… "},
		{"🇧🇷🇧🇷", 3, "🇧🇷…"},
		{"x", 0, ""},
		{"a\tb", 3, "a b"},
	} {
		if got := Fit(tc.s, tc.w); got != tc.want {
			t.Errorf("Fit(%q, %d) = %q, want %q", tc.s, tc.w, got, tc.want)
		}
	}
	if got := FitRight("9d", 4); got != "  9d" {
		t.Errorf("FitRight: %q", got)
	}
	if got := Cut("abcdef", 4); got != "abc…" {
		t.Errorf("Cut: %q", got)
	}
	if got := Cut("ab", 4); got != "ab" {
		t.Errorf("Cut pads: %q", got)
	}
	if got := Suffix("日本語", 4); got != "本語" {
		t.Errorf("Suffix: %q", got)
	}
}

func TestCleanDropsWhatReordersALine(t *testing.T) {
	got := Clean("evil\x1b[2J \u009bname \u202eolleh\u2028x\u200d")
	if strings.ContainsAny(got, "\x1b\u009b\u202e\u2028") || !strings.Contains(got, "\u200d") {
		t.Fatalf("%q", got)
	}
}
