package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/mattn/go-runewidth"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/termfmt"
)

// Titles from the wild: gitmoji, emoji with VS16, a ZWJ family, CJK,
// combining marks, a flag.
var wildTitles = []string{
	"✨ feat: ⚡️ faster ✅ ❌ ⭐ ⌚ ⏳ ♻️ ⬆️ 🚀 e\u0301",
	"日本語のタイトルがとても長い 한국어 中文 ｆｕｌｌ",
	"👨\u200d👩\u200d👧\u200d👦 family 🇧🇷 flag ♈ ⚓ ⛔ 🀄",
	strings.Repeat("long title ", 30),
}

func TestRenderNeverExceedsTheWidth(t *testing.T) {
	for _, c := range [][2]bool{{false, false}, {true, false}, {false, true}, {true, true}} {
		eastAsian, color := c[0], c[1]
		t.Cleanup(termfmt.UseWidths(&runewidth.Condition{EastAsianWidth: eastAsian, StrictEmojiNeutral: true}))
		measure := &runewidth.Condition{EastAsianWidth: eastAsian, StrictEmojiNeutral: true}
		m := newModel(t)
		m.SetColor(color)
		var rs []api.Row
		for i, title := range wildTitles {
			r := row(7+i, title, "review", "needs-me")
			r.Summary.Reason = title
			setPR(&r, func(p *prView) { p.Workspace.DisplayName = title })
			r.Link, r.Summary.Stalled = "https://github.com/acme/api/pull/7", i%2 == 0
			rs = append(rs, r)
		}
		m.SetData(rs, api.Health{Paused: true, SlowSync: true, Warnings: []string{wildTitles[1]}})
		press(m, Key{Rune: 'V'}, Key{Name: KeyPaste, Text: wildTitles[0] + wildTitles[2]})
		for _, size := range [][2]int{{20, 12}, {40, 12}, {59, 12}, {60, 12}, {80, 30}, {99, 12}, {100, 12}, {120, 40}, {139, 12}, {140, 12}, {200, 40}} {
			w, h := size[0], size[1]
			lines := m.Render(w, h, now)
			if len(lines) != h {
				t.Fatalf("width %d: %d lines", w, len(lines))
			}
			for i, l := range lines {
				if c := measure.StringWidth(termfmt.Plain(l)); c > w {
					t.Errorf("east asian %v, color %v, width %d, line %d has %d cells: %q", eastAsian, color, w, i, c, l)
				}
			}
		}
	}
}

func TestRenderSurvivesANonsenseSize(t *testing.T) {
	m := newModel(t)
	m.SetData(rows(), api.Health{})
	for _, size := range [][2]int{{80, -1}, {-5, 10}, {0, 0}, {19, 4}} {
		if lines := m.Render(size[0], size[1], now); len(lines) != max(size[1], 0) {
			t.Errorf("%v: %d lines", size, len(lines))
		}
	}
}

func TestRenderMarksASelectedRowThatIsBusy(t *testing.T) {
	m := newModel(t)
	m.SetData(rows(), api.Health{})
	press(m, Key{Rune: 'O'})
	if l := m.Render(100, 8, now)[2]; !strings.HasPrefix(l, ">* ") || !strings.Contains(l, "acme/api#7  Batch export") {
		t.Fatalf("%q", l)
	}
}

func TestRenderStripsControlCharacters(t *testing.T) {
	m := newModel(t)
	m.SetData([]api.Row{row(7, "evil\x1b[2J\x1b]0;title\x07 \u009bname \u202eolleh\u2028\u2066x\u200f\ufeff", "review", "needs-me")}, api.Health{})
	for _, l := range m.Render(100, 8, now) {
		if strings.ContainsAny(l, "\x1b\x07\u009b\u202e\u2028\u2066\u200f\ufeff") {
			t.Fatalf("control character in %q", l)
		}
	}
}

func TestRenderShowsTheViewCountdownAndHelp(t *testing.T) {
	m := newModel(t)
	pending := row(3, "Pending round", "round_pending", "needs-me")
	stalled := row(8, "Stalled one", "changes", "needs-me", "mine")
	stalled.Summary.Stalled = true
	stalled.Summary.Since = now.Add(-50 * time.Hour)
	m.SetData([]api.Row{pending, stalled}, api.Health{Warnings: []string{"app: notifications are off"}})
	m.SetGates([]api.GateView{planGate(pending, now.Add(42*time.Second)), {ID: 2, Unit: stalled.Unit.String(), Kind: "question", Status: "open"}})
	screen := strings.Join(m.Render(120, 10, now), "\n")
	for _, want := range []string{"[Needs me 2]", "My PRs 1", "In loop 0", "acme/api#3  Pending round", "42s", "!2d", "round soon", "changes asked", "warning: app: notifications are off", "V review", "q quit"} {
		if !strings.Contains(screen, want) {
			t.Errorf("screen lacks %q:\n%s", want, screen)
		}
	}
	press(m, Key{Rune: 'V'}, Key{Rune: 'h'}, Key{Rune: 'i'})
	if last := m.Render(120, 10, now)[9]; !strings.HasPrefix(last, "review acme/api#3: Guidance (what the round should look at) (Enter sends, Esc cancels): hi") {
		t.Fatalf("prompt line %q", last)
	}
}

func TestRenderEmptyAndLoading(t *testing.T) {
	m := newModel(t)
	if s := strings.Join(m.Render(80, 6, now), "\n"); !strings.Contains(s, "loading") {
		t.Fatalf("before data:\n%s", s)
	}
	m.SetData(nil, api.Health{})
	if s := strings.Join(m.Render(80, 6, now), "\n"); !strings.Contains(s, "Nothing in Needs me") {
		t.Fatalf("empty:\n%s", s)
	}
}

func TestRenderNamesTheAuthorItWaitsOn(t *testing.T) {
	m := newModel(t)
	theirs := row(7, "Theirs", "changes", "needs-me")
	theirs.Summary.WaitsOn = "author, carla"
	mine := row(8, "Mine", "changes", "needs-me")
	mine.Summary.WaitsOn = "author"
	setPR(&mine, func(p *prView) { p.Author = "Me" })
	setPR(&mine, func(p *prView) { p.Workspace.Status = "in-progress" })
	m.SetData([]api.Row{theirs, mine}, api.Health{Viewer: "me"})
	press(m, Key{Name: KeyDown})
	screen := strings.Join(m.Render(160, 16, now), "\n")
	for _, want := range []string{"ana, carla · changes", "me · changes asked", "in-progress"} {
		if !strings.Contains(screen, want) {
			t.Errorf("screen lacks %q:\n%s", want, screen)
		}
	}
	if strings.Contains(screen, " · in-progress") {
		t.Errorf("a workspace without a name leads with a separator:\n%s", screen)
	}
}

func TestRenderShowsTheFilterAndCountsMatches(t *testing.T) {
	m := newModel(t)
	m.SetData(rows(), api.Health{})
	press(m, append([]Key{{Rune: '/'}}, typed("cache")...)...)
	lines := m.Render(100, 10, now)
	if !strings.Contains(lines[0], "Needs me 1") || !strings.Contains(lines[0], "In loop 0") {
		t.Fatalf("tabs: %q", lines[0])
	}
	if last := lines[len(lines)-1]; !strings.HasPrefix(last, "/cache_") {
		t.Fatalf("bottom: %q", last)
	}
	press(m, Key{Name: KeyEnter})
	if last := m.Render(100, 10, now)[9]; !strings.HasPrefix(last, "filter cache (Esc clears)") {
		t.Fatalf("bottom after enter: %q", last)
	}
	for _, l := range m.Render(30, 10, now) {
		if termfmt.Cells(l) > 30 {
			t.Fatalf("line wider than 30: %q", l)
		}
	}
}

func TestANarrowFilterLineKeepsTheCursor(t *testing.T) {
	m := newModel(t)
	m.SetData(rows(), api.Health{})
	press(m, append([]Key{{Rune: '/'}}, typed("a long filter text")...)...)
	for _, w := range []int{20, 24, 26, 40} {
		last := m.Render(w, 8, now)[7]
		if !strings.Contains(last, "text_") || termfmt.Cells(last) > w {
			t.Errorf("width %d: %q", w, last)
		}
	}
	if last := m.Render(60, 8, now)[7]; !strings.Contains(last, "text_  Enter keeps, Esc clears") {
		t.Errorf("width 60 lost the hint: %q", last)
	}
}

func TestRenderShowsSizeAndRiskFromAHundredColumns(t *testing.T) {
	m := newModel(t)
	rs := rows()
	setPR(&rs[0], func(p *prView) { p.Brief = &api.BriefSummary{Size: "m", Risk: "high", Goal: "Export in batches"} })
	setPR(&rs[1], func(p *prView) { p.Brief = &api.BriefSummary{Size: "xl", Risk: "medium"} })
	m.SetData(rs, api.Health{})
	lines := m.Render(100, 8, now)
	if !strings.Contains(lines[2], " M·hi ") || !strings.Contains(lines[3], " XL·md ") {
		t.Fatalf("at 100 columns:\n%s", strings.Join(lines, "\n"))
	}
	for _, w := range []int{60, 99} {
		lines := m.Render(w, 8, now)
		if strings.Contains(strings.Join(lines, "\n"), "M·hi") {
			t.Fatalf("at %d columns:\n%s", w, strings.Join(lines, "\n"))
		}
	}
}
