package termfmt

import (
	"strings"
	"testing"
)

func TestColumnsCutTheFlexibleColumnOnANarrowWidth(t *testing.T) {
	cols := []Column{{Right: true}, {}, {Flex: true}, {Max: 8}}
	rows := [][]Cell{
		{{Text: "9d"}, {Text: "acme/api#1365"}, {Text: "日本語のタイトルがとても長い 한국어"}, {Text: "author"}},
		{{Text: "38h"}, {Text: "acme/web#4"}, {Text: "✨ feat: ⚡️ faster ♻️ 🇧🇷"}, {Text: "team:acme/reviewers"}},
	}
	for _, color := range []bool{false, true} {
		tm := Term{Color: color}
		styled := [][]Cell{rows[0], rows[1]}
		styled[0][0].Style = tm.Yellow
		for _, w := range []int{30, 41, 60} {
			lines := tm.Columns(w, 2, cols, styled)
			if len(lines) != 2 {
				t.Fatalf("%d lines", len(lines))
			}
			for _, l := range lines {
				if c := Cells(Plain(l)); c > w {
					t.Errorf("color %v width %d: %d cells: %q", color, w, c, Plain(l))
				}
			}
			p0, p1 := Plain(lines[0]), Plain(lines[1])
			if !strings.HasPrefix(p0, " 9d  acme/api#1365  ") || !strings.HasPrefix(p1, "38h  acme/web#4     ") {
				t.Errorf("width %d: fixed columns not aligned:\n%s\n%s", w, p0, p1)
			}
			if !strings.HasSuffix(p1, "team:ac…") {
				t.Errorf("width %d: the capped column is not cut: %q", w, p1)
			}
		}
	}
	wide := Term{}.Columns(200, 2, cols, rows)
	if !strings.Contains(wide[0], "日本語のタイトルがとても長い 한국어  author") || strings.HasSuffix(wide[0], " ") {
		t.Errorf("a wide terminal cuts or pads: %q", wide[0])
	}
}

func TestWrapHangsWithinTheWidths(t *testing.T) {
	got := Wrap("agent=claude review_prefix=REVIEW views.orca.columns.working=reviewing brief=true", 30, 20)
	want := []string{"agent=claude", "review_prefix=REVIEW", "views.orca.columns.w", "orking=reviewing", "brief=true"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("got %q", got)
	}
	for _, l := range Wrap("日本語 のタイトルがとても長い 한국어 ✨ feat", 10, 7) {
		if Cells(l) > 10 {
			t.Errorf("%q exceeds", l)
		}
	}
	if got := Wrap("", 10, 10); len(got) != 0 {
		t.Errorf("empty: %q", got)
	}
}

func TestWrapCutsAWideClusterThatFitsNoLine(t *testing.T) {
	if got := Wrap("日本", 1, 1); strings.Join(got, "|") != "日|本" {
		t.Errorf("%q", got)
	}
}
