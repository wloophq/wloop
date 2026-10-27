package keys

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
)

type row struct {
	key      string
	action   string
	context  Context
	surfaces []Surface
}

var (
	all    = []Surface{Web, TUI, Menubar}
	screen = []Surface{Web, TUI}
)

// spec92 is spec 9.2's table, with the contract's TUI, Review and Plan rows.
func spec92() []row {
	rows := []row{
		{"j", "move:down", List, screen},
		{"k", "move:up", List, screen},
		{"Enter", "detail:open", List, screen},
		{"Esc", "detail:close", List, screen},
		{"x", "next", List, all},
		{"w", "waiting", List, all},
		{"s", "schedule", List, all},
		{"y", "someday", List, all},
		{"l", "reference", List, all},
		{"f", "focus", List, all},
		{"p", "project", List, all},
		{"a", "approve", List, all},
		{"d", "delegate", List, all},
		{"e", "done", List, all},
		{"Backspace", "dismiss", List, all},
		{"r", "reply", List, screen},
		{"i", "edit", List, []Surface{TUI}},
		{"o", "open-source", List, all},
		{"n", "capture", Global, screen},
		{"g f", "nav:focus", Global, screen},
		{"g i", "nav:inbox", Global, screen},
		{"g o", "nav:outbox", Global, screen},
		{"g n", "nav:next", Global, screen},
		{"g w", "nav:waiting", Global, screen},
		{"g p", "nav:projects", Global, screen},
		{"g r", "nav:review", Global, screen},
		{"g g", "nav:github", Global, screen},
		{"/", "filter", Global, screen},
		{"Cmd+K", "palette", Global, []Surface{Web}},
		{"Ctrl+K", "palette", Global, []Surface{TUI}},
		{"?", "help", Global, screen},
		{"q", "quit", Global, []Surface{TUI}},
		{"Tab", "nav:cycle", Global, []Surface{TUI}},
		{"Ctrl+C", "quit", Global, []Surface{TUI}},
		{"Enter", "step:done", Review, screen},
		{"]", "step:next", Review, screen},
		{"[", "step:prev", Review, screen},
		{"Enter", "approve", Plan, screen},
	}
	for n := 1; n <= 9; n++ {
		rows = append(rows,
			row{fmt.Sprint(n), fmt.Sprintf("answer:%d", n), Question, screen},
			row{fmt.Sprintf("Shift+%d", n), fmt.Sprintf("area:%d", n), Global, screen})
	}
	return rows
}

func TestCoreMatchesTheSpec(t *testing.T) {
	want := spec92()
	var got []row
	for _, b := range Core {
		got = append(got, row{b.Key, b.Action, b.Context, b.Surfaces})
	}
	id := func(r row) string { return fmt.Sprintf("%s|%s|%s|%v", r.key, r.action, r.context, r.surfaces) }
	g, w := map[string]bool{}, map[string]bool{}
	for _, r := range got {
		g[id(r)] = true
	}
	for _, r := range want {
		w[id(r)] = true
		if !g[id(r)] {
			t.Errorf("missing %s", id(r))
		}
	}
	for _, r := range got {
		if !w[id(r)] {
			t.Errorf("not in the spec %s", id(r))
		}
	}
	for _, b := range Core {
		if c, err := Canonical(b.Key); err != nil || c != b.Key {
			t.Errorf("%q is not canonical: %q %v", b.Key, c, err)
		}
		if len(b.Key) == 1 && b.Key >= "1" && b.Key <= "9" && b.Context != Question {
			t.Errorf("digit %s outside the Question context", b.Key)
		}
	}
}

func TestCanonical(t *testing.T) {
	for in, want := range map[string]string{
		"shift+r": "R", "Shift+R": "R", "R": "R", "r": "r", "SHIFT+r": "R",
		"enter": "Enter", "Return": "Enter", "esc": "Esc", "Escape": "Esc", "backspace": "Backspace", "tab": "Tab",
		"ctrl+k": "Ctrl+K", "Cmd+k": "Cmd+K", "ctrl+C": "Ctrl+C",
		"?": "?", "/": "/", "]": "]", "1": "1", "shift+1": "Shift+1",
		"g f": "g f", "G  F": "", "g shift+f": "g F",
	} {
		got, err := Canonical(in)
		if want == "" {
			if err == nil {
				t.Errorf("Canonical(%q) = %q, want an error", in, got)
			}
			continue
		}
		if err != nil || got != want {
			t.Errorf("Canonical(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"Hyper+x", "", "shift+", "ctrl+", "Shift+?", "ab", "é", "Ctrl+Shift+K", " "} {
		if got, err := Canonical(bad); err == nil {
			t.Errorf("Canonical(%q) = %q, want an error", bad, got)
		}
	}
	if _, err := Canonical("Hyper+x"); err == nil || !strings.Contains(err.Error(), "Hyper+x") {
		t.Errorf("the error does not name the key: %v", err)
	}
}

func TestReservedPerSurface(t *testing.T) {
	r := Reserved(TUI)
	for _, k := range []string{"q", "Tab", "Ctrl+C", "Ctrl+K", "g", "g f", "Enter", "Esc", "Backspace", "/", "?", "[", "]", "Shift+1", "Shift+9"} {
		if !r[k] {
			t.Errorf("tui: %q is not reserved", k)
		}
	}
	for c := 'a'; c <= 'z'; c++ {
		if !r[string(c)] {
			t.Errorf("tui: %c is not reserved", c)
		}
	}
	for c := '0'; c <= '9'; c++ {
		if !r[string(c)] {
			t.Errorf("tui: %c is not reserved", c)
		}
	}
	for _, b := range Core {
		if slices.Contains(b.Surfaces, TUI) && !r[b.Key] {
			t.Errorf("tui: the core key %q is not reserved", b.Key)
		}
	}
	for c := 'A'; c <= 'Z'; c++ {
		if r[string(c)] {
			t.Errorf("tui: %c is reserved", c)
		}
	}
	web := Reserved(Web)
	if !web["Cmd+K"] || web["Ctrl+K"] || web["q"] != true || web["Tab"] {
		t.Errorf("web: %v", web)
	}
	r["Z"] = true
	if Reserved(TUI)["Z"] {
		t.Fatal("Reserved hands out a shared map")
	}
}

func TestKeyCollidingWithCoreIsRefused(t *testing.T) {
	err := Check(Web, "x", "acme.act", nil, nil)
	if !errors.Is(err, ErrReserved) || err.Error() != "x is the core key of next on web" {
		t.Fatalf("%v", err)
	}
	for _, k := range []string{"b", "7", "shift+3", "?", "Enter", "g"} {
		if err := Check(TUI, k, "acme.act", nil, nil); !errors.Is(err, ErrReserved) {
			t.Errorf("%s: %v", k, err)
		}
	}
	if err := Check(TUI, "b", "acme.act", nil, nil); err.Error() != "b is reserved by the core on tui" {
		t.Errorf("%v", err)
	}
	for _, k := range []string{"!", "Ctrl+J", "Hyper+x"} {
		err := Check(Web, k, "acme.act", nil, nil)
		if err == nil || !strings.Contains(err.Error(), "web") {
			t.Errorf("%s: %v", k, err)
		}
	}
	if err := Check(Web, "shift+q", "acme.act", nil, nil); err != nil {
		t.Errorf("Shift+q: %v", err)
	}
}

func TestKeysCollideWithEachOther(t *testing.T) {
	claimed := map[string]string{}
	if err := Check(TUI, "H", "hold", claimed, nil); err != nil {
		t.Fatal(err)
	}
	claimed["H"] = "hold"
	err := Check(TUI, "H", "acme.hush", claimed, nil)
	if err == nil || err.Error() != "H is the key of hold on tui" || errors.Is(err, ErrReserved) {
		t.Fatalf("%v", err)
	}
	if err := Check(TUI, "shift+h", "acme.hush", claimed, nil); err == nil {
		t.Fatal("a spelling of a claimed key passed")
	}
	if err := Check(TUI, "H", "hold", claimed, nil); err != nil {
		t.Fatalf("an owner collides with itself: %v", err)
	}
}

func TestToggleSidesShareAKey(t *testing.T) {
	toggles := map[string]string{"hold": "release", "release": "hold"}
	claimed := map[string]string{"H": "hold"}
	if err := Check(TUI, "H", "release", claimed, toggles); err != nil {
		t.Fatal(err)
	}
	if err := Check(TUI, "H", "release", claimed, map[string]string{"hold": "release"}); err != nil {
		t.Fatalf("one side of the pair: %v", err)
	}
	if err := Check(TUI, "H", "mute", claimed, toggles); err == nil {
		t.Fatal("a third action took the pair's key")
	}
}

func TestSurfacesOf(t *testing.T) {
	got := SurfacesOf([]string{"cli", "tui", "row", "detail", "notification", "menubar", "explore"})
	if !slices.Equal(got, []Surface{Web, TUI, Menubar}) {
		t.Fatalf("%v", got)
	}
	if got := SurfacesOf([]string{"cli"}); len(got) != 0 {
		t.Fatalf("%v", got)
	}
}
