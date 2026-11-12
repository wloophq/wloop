package web

import (
	"strings"
	"testing"
)

// S4 on the card: the agent recommends its pinned option, the person types a change request and presses
// Answer without touching the options. The card sends the recommended option with the text, which the gates'
// option hook reads as a pick of the pinned draft and approves it: a free answer posts the draft.
func TestReattackFreeAnswerSendsThePinnedRecommendation(t *testing.T) {
	var got struct{ Posts []any }
	runGate(t, `
const c = card(input.gate);
focus(c, "gate:3:text").on.input({ target: { value: "rewrite the second finding" } });
await focus(c, "gate:3:answer").on.click();
done({ Posts: posts });`, map[string]any{"gate": pinnedQuestion(t, 3, false)}, &got)
	body := mustJS(t, got.Posts)
	if strings.Contains(body, `"answer":"post\n`) || strings.Contains(body, `"answer":"post\\n`) {
		t.Errorf("a typed change request posted %s: the recommended pinned option rides along and approves draft 7", body)
	}
}

// A pinned option is sent when the person picks it, with the text typed.
func TestAPickedPinnedOptionIsSent(t *testing.T) {
	var got struct{ Posts []any }
	runGate(t, `
let c = card(input.gate);
await focus(c, "gate:3:option:post").on.click();
c = card(input.gate);
focus(c, "gate:3:text").on.input({ target: { value: "and thank them" } });
await focus(c, "gate:3:answer").on.click();
done({ Posts: posts });`, map[string]any{"gate": pinnedQuestion(t, 3, false)}, &got)
	if body := mustJS(t, got.Posts); !strings.Contains(body, `"answer":"post\nand thank them"`) {
		t.Errorf("a picked pinned option posted %s", body)
	}
}
