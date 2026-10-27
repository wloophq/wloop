package keys

import "fmt"

var (
	everySurface  = []Surface{Web, TUI, Menubar}
	screens       = []Surface{Web, TUI}
	webOnly       = []Surface{Web}
	terminalOnly  = []Surface{TUI}
	chordPrefixes = []string{"g"}
)

// Core are the core bindings of spec 9.2.
var Core = core()

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
		{"g f", "nav:focus", Global, screens},
		{"g i", "nav:inbox", Global, screens},
		{"g o", "nav:outbox", Global, screens},
		{"g n", "nav:next", Global, screens},
		{"g w", "nav:waiting", Global, screens},
		{"g p", "nav:projects", Global, screens},
		{"g r", "nav:review", Global, screens},
		{"g g", "nav:github", Global, screens},
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
