package web

import (
	"strings"
	"testing"
)

// TestTheRoundChromeReadsTheServedGateIntents: the focus a round's buttons
// keep and the guidance box's words come from the gate intents the daemon
// serves, so a profile that renames them changes the chrome.
func TestTheRoundChromeReadsTheServedGateIntents(t *testing.T) {
	src := appJS(t)
	for _, name := range []string{`"run_now"`, `"skip"`, "Run now"} {
		if strings.Contains(src, name) {
			t.Errorf("app.js names the bundled plan gate intent %s", name)
		}
	}
	prelude := jsConst(t, src, "FOCUS_PAIRS") + `const state = { specs: { byId: {
  go_now: { id: "go_now", label: "Go now", group: "start", verb: "approve", intake: true },
  drop: { id: "drop", label: "Drop", group: "start", verb: "dismiss" },
  look: { id: "look", label: "Look", group: "start", intake: true },
  pause: { id: "pause", label: "Pause", group: "control" },
} } };
const roundGate = (r) => r.round;
`
	var got struct {
		Slots   []string `json:"slots"`
		Pending string   `json:"pending"`
		Next    string   `json:"next"`
	}
	nodeRun(t, prelude, []string{"focusSlot", "guidanceLabel"}, `process.stdout.write(JSON.stringify({
  slots: ["go_now", "look", "drop", "pause", "resume"].map(focusSlot),
  pending: guidanceLabel({ round: {} }),
  next: guidanceLabel({}),
}));`, nil, &got)
	if want := []string{"start", "start", "open", "pause", "pause"}; strings.Join(got.Slots, ",") != strings.Join(want, ",") {
		t.Errorf("slots %v, want %v", got.Slots, want)
	}
	if got.Pending != "Guidance for the pending round; Go now sends it at once" || got.Next != "Guidance for the next round (optional)" {
		t.Errorf("guidance labels %q %q", got.Pending, got.Next)
	}
}
