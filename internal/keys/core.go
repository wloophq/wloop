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
		{"j", "move:down", List, screens, "move down"},
		{"k", "move:up", List, screens, "move up"},
		{"Enter", "detail:open", List, screens, "open the detail"},
		{"Esc", "detail:close", List, screens, "close the detail"},
		{"x", "next", List, everySurface, "to Next"},
		{"w", "waiting", List, everySurface, "to Waiting"},
		{"s", "schedule", List, everySurface, "schedule or snooze"},
		{"y", "someday", List, everySurface, "to Someday"},
		{"l", "reference", List, everySurface, "to Reference"},
		{"f", "focus", List, everySurface, "toggle Focus"},
		{"p", "project", List, everySurface, "make a project or attach to one"},
		{"a", "approve", List, everySurface, "approve the draft or proposal"},
		{"d", "delegate", List, everySurface, "delegate to an agent"},
		{"e", "done", List, everySurface, "done"},
		{"Backspace", "dismiss", List, everySurface, "dismiss"},
		{"r", "reply", List, screens, "reply in the conversation"},
		{"i", "edit", List, terminalOnly, "edit the draft's text"},
		{"o", "open-source", List, everySurface, "open the source"},
		{"n", "capture", Global, screens, "capture"},
		{"g f", "nav:focus", Global, screens, "Focus"},
		{"g i", "nav:inbox", Global, screens, "Inbox"},
		{"g o", "nav:outbox", Global, screens, "Outbox"},
		{"g n", "nav:next", Global, screens, "Next"},
		{"g w", "nav:waiting", Global, screens, "Waiting"},
		{"g p", "nav:projects", Global, screens, "Projects"},
		{"g r", "nav:review", Global, screens, "Review"},
		{"g g", "nav:github", Global, screens, "the GitHub views"},
		{"/", "filter", Global, screens, "filter"},
		{"Cmd+K", "palette", Global, webOnly, "every action and its key"},
		{"Ctrl+K", "palette", Global, terminalOnly, "every action and its key"},
		{"?", "help", Global, screens, "help"},
		{"q", "quit", Global, terminalOnly, "quit"},
		{"Tab", "nav:cycle", Global, terminalOnly, "next view in the group"},
		{"Ctrl+C", "quit", Global, terminalOnly, "quit"},
		{"Enter", "step:done", Review, screens, "mark the step done"},
		{"]", "step:next", Review, screens, "next step"},
		{"[", "step:prev", Review, screens, "previous step"},
		{"Enter", "approve", Plan, screens, "apply the selected plan step"},
	}
	for n := 1; n <= 9; n++ {
		b = append(b,
			Binding{fmt.Sprint(n), fmt.Sprintf("answer:%d", n), Question, screens, fmt.Sprintf("answer option %d", n)},
			Binding{fmt.Sprintf("Shift+%d", n), fmt.Sprintf("area:%d", n), Global, screens, fmt.Sprintf("area %d", n)})
	}
	return b
}
