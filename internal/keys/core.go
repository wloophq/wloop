package keys

import (
	"fmt"
	"slices"
	"strings"
)

var (
	everySurface  = []Surface{Web, TUI, Menubar}
	screens       = []Surface{Web, TUI}
	webOnly       = []Surface{Web}
	terminalOnly  = []Surface{TUI}
	chordPrefixes = []string{"g"}
)

// Core are the core bindings of spec 9.2. The keys that jump to a view are the catalog's (Views); a digit in a
// gate runs the gate's intent of that order (answer:<n>).
var Core = core()

// View is a view the catalog serves: its id and the key that jumps to it, "" for none.
type View struct{ ID, Key string }

// Views are the bindings that jump to the views of vs that have a key, in their order, on the screens.
func Views(vs []View) []Binding {
	var out []Binding
	for _, v := range vs {
		if v.Key != "" {
			out = append(out, Binding{v.Key, "nav:" + v.ID, Global, screens})
		}
	}
	return out
}

// CheckView refuses a view's key: not a chord of a chord prefix, or a core key on a screen.
func CheckView(key string) error {
	k, err := Canonical(key)
	if err != nil {
		return err
	}
	prefix, _, chord := strings.Cut(k, " ")
	if !chord || !slices.Contains(chordPrefixes, prefix) {
		return fmt.Errorf("%s is not a chord: a view's key is %s, a space and a key", k, strings.Join(chordPrefixes, " or "))
	}
	for _, b := range Core {
		if b.Key == k {
			return reservedError{fmt.Sprintf("%s is the core key of %s", k, b.Action)}
		}
	}
	return nil
}

func core() []Binding {
	b := []Binding{
		{"j", "move:down", List, screens},
		{"k", "move:up", List, screens},
		{"Enter", "detail:open", List, screens},
		{"Esc", "detail:close", List, screens},
		{"x", "next", List, everySurface},
		{"w", "waiting", List, everySurface},
		{"s", "schedule", List, everySurface},
		{"y", "someday", List, everySurface},
		{"l", "reference", List, everySurface},
		{"f", "focus", List, everySurface},
		{"p", "project", List, everySurface},
		{"a", "approve", List, everySurface},
		{"d", "delegate", List, everySurface},
		{"e", "done", List, everySurface},
		{"Backspace", "dismiss", List, everySurface},
		{"r", "reply", List, screens},
		{"i", "edit", List, terminalOnly},
		{"o", "open-source", List, everySurface},
		{"n", "capture", Global, screens},
		{"/", "filter", Global, screens},
		{"Cmd+K", "palette", Global, webOnly},
		{"Ctrl+K", "palette", Global, terminalOnly},
		{"?", "help", Global, screens},
		{"q", "quit", Global, terminalOnly},
		{"Tab", "nav:cycle", Global, terminalOnly},
		{"Ctrl+C", "quit", Global, terminalOnly},
		{"Enter", "step:done", Review, screens},
		{"]", "step:next", Review, screens},
		{"[", "step:prev", Review, screens},
		{"Enter", "approve", Plan, screens},
	}
	for n := 1; n <= 9; n++ {
		b = append(b,
			Binding{fmt.Sprint(n), fmt.Sprintf("answer:%d", n), Question, screens},
			Binding{fmt.Sprintf("Shift+%d", n), fmt.Sprintf("area:%d", n), Global, screens})
	}
	return b
}
