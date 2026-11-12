package web

import (
	"slices"
	"strings"
	"testing"
)

// TestAnActionButtonIsOffWhileItsRequestIsOnItsWay keeps a draft's approve button off, on the card drawn while
// its request is on its way as on the one clicked, and takes a second click as nothing; the answer turns it on.
func TestAnActionButtonIsOffWhileItsRequestIsOnItsWay(t *testing.T) {
	var got struct {
		Clicked, Redrawn, After bool
		Posts                   int
	}
	runGate(t, `
let answer;
detailEnv.post = (path, body) => { posts.push({ path, body }); return new Promise((resolve) => { answer = resolve; }); };
const off = (b) => "disabled" in b.attrs;
const button = focus(draft(input.draft), "outbox:approve:draft:7");
const running = button.on.click();
const clicked = off(button);
const redrawn = off(focus(draft(input.draft), "outbox:approve:draft:7"));
focus(draft(input.draft), "outbox:approve:draft:7").on.click();
answer({});
await running;
done({ Clicked: clicked, Redrawn: redrawn, After: off(focus(draft(input.draft), "outbox:approve:draft:7")), Posts: posts.length });`,
		map[string]any{"draft": pendingDraft()}, &got)
	if !got.Clicked || !got.Redrawn {
		t.Errorf("while on its way the clicked button is off %v and the redrawn one %v, want both off", got.Clicked, got.Redrawn)
	}
	if got.After || got.Posts != 1 {
		t.Errorf("after the answer the button is off %v, %d requests: want it on and one request", got.After, got.Posts)
	}
}

// TestAnActionsResultShowsBesideItsButton draws a refused approval's error under the draft card's buttons, not at
// the top of the page, and the gate card's refusal under its own buttons.
func TestAnActionsResultShowsBesideItsButton(t *testing.T) {
	var got struct {
		Draft, Gate []string
		Top         []string
	}
	runGate(t, `
detailEnv.notice = (text, err) => { if (!actionNotice(text, err)) notices.push(text); };
detailEnv.post = async () => { throw new Error("the draft is stale: the unit changed since it was written"); };
await focus(draft(input.draft), "outbox:approve:draft:7").on.click();
const results = (c) => cls(c, "act-result").map((x) => attr(x, "class") + " " + x.textContent);
await focus(card(input.gate), "gate:5:approve").on.click();
done({ Draft: results(draft(input.draft)), Gate: results(card(input.gate)), Top: notices });`,
		map[string]any{"draft": pendingDraft(), "gate": stepGate()}, &got)
	if len(got.Draft) != 1 || !strings.HasPrefix(got.Draft[0], "act-result err ") || !strings.Contains(got.Draft[0], "the draft is stale") {
		t.Errorf("the draft card's results %q, want the refusal beside its buttons", got.Draft)
	}
	if len(got.Gate) != 1 || !strings.Contains(got.Gate[0], "the draft is stale") {
		t.Errorf("the gate card's results %q, want its refusal beside its buttons", got.Gate)
	}
	if slices.ContainsFunc(got.Top, func(s string) bool { return strings.Contains(s, "stale") }) {
		t.Errorf("the refusal also went to the top: %q", got.Top)
	}
}
