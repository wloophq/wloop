package web

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/domain/record"
)

// pinnedQuestion is a question gate whose "post" option and confirm pin review draft 7 at rev 3, served with
// the draft as its card serves it at rev.
func pinnedQuestion(t *testing.T, rev int64, confirm bool) api.GateView {
	t.Helper()
	d := reviewDraft()
	d.Rev, d.Preview = rev, "Request changes with 2 inline comments"
	pin := &record.DraftRef{ID: 7, Rev: 3}
	m := api.Message{ID: 3, Unit: prUnit, Kind: "question", Text: "Hold, post nothing?", Pinned: []api.Draft{d}}
	if confirm {
		m.Confirm, m.Draft = true, pin
	} else {
		m.Recommend, m.Options = "post", []record.Option{{ID: "post", Label: "Hold, post nothing", Draft: pin}, {ID: "wait", Label: "Wait"}}
	}
	subject, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return api.GateView{ID: 3, Task: "t-1", Kind: "question", Name: "question", Status: "open", Text: "The agent asks", Subject: subject,
		Intents: []api.IntentView{{ID: "answer", Label: "Answer", Answer: true}, {ID: "dismiss", Label: "Dismiss"}}}
}

// TestAPinnedOptionShowsItsDraft shows, under the option whose pick approves a draft and under a pinned confirm,
// the draft itself: its preview, its text and every value of its payload, never only the agent's label; and says
// when it changed since the question pinned it.
func TestAPinnedOptionShowsItsDraft(t *testing.T) {
	var got struct {
		Pins, Confirm    []string
		Values, Changed  int
		Unchanged, Notes []string
	}
	runGate(t, `
const pins = (c) => cls(c, "d-pin");
const c = card(input.option);
const same = card(input.same);
done({ Pins: pins(c).map((p) => attr(p, "data-draft") + "@" + attr(p, "data-rev") + " " + cls(p, "d-preview").map((x) => x.textContent).join("")),
  Confirm: pins(card(input.confirm)).map((p) => attr(p, "data-draft")),
  Values: pins(c).flatMap((p) => cls(p, "pv")).length, Changed: pins(c).flatMap((p) => cls(p, "err")).length,
  Unchanged: pins(same).flatMap((p) => cls(p, "err")).map((x) => x.textContent), Notes: pins(c).flatMap((p) => cls(p, "d-note")).map((x) => x.textContent) });`,
		map[string]any{"option": pinnedQuestion(t, 4, false), "same": pinnedQuestion(t, 3, false), "confirm": pinnedQuestion(t, 3, true)}, &got)
	if !slices.Equal(got.Pins, []string{"7@3 Request changes with 2 inline comments"}) {
		t.Errorf("pins %q, want draft 7 at rev 3 with its preview under the pinned option only", got.Pins)
	}
	if !slices.Equal(got.Confirm, []string{"7"}) {
		t.Errorf("confirm pins %q, want draft 7", got.Confirm)
	}
	if got.Values != len(leaves(reviewDraft().Fields)) {
		t.Errorf("%d payload values under the pin, want all %d", got.Values, len(leaves(reviewDraft().Fields)))
	}
	if got.Changed != 1 || len(got.Unchanged) != 0 || !strings.Contains(strings.Join(got.Notes, "|"), "rev 4") {
		t.Errorf("changed notes %d, unchanged %q, notes %q: want one note naming rev 4 only when it changed", got.Changed, got.Unchanged, got.Notes)
	}
}

// TestARowsPinnedAnswerIsNoButton offers no one-click answer on a row that approves a pinned draft, which the row
// does not show, and says the question's task shows it.
func TestARowsPinnedAnswerIsNoButton(t *testing.T) {
	var got struct {
		Choice, Confirm []string
		Pins            bool
	}
	runGate(t, `
const row = { unit: { provider: "github", key: "acme/api#7" }, ref: { owner: "acme", repo: "api", number: 7 }, actions: [{ id: "answer", enabled: true }] };
const labels = (bs) => bs.map((b) => b.textContent);
done({ Choice: labels(questionChoices(row, input.choice)), Confirm: labels(questionChoices(row, input.confirm)), Pins: questionPins(input.confirm) });`,
		map[string]any{
			"choice":  api.QuestionView{State: "asked", Kind: "choice", Options: []record.Option{{ID: "post", Label: "Post it", Draft: &record.DraftRef{ID: 7, Rev: 3}}, {ID: "wait", Label: "Wait"}}},
			"confirm": api.QuestionView{State: "asked", Kind: "confirm", Draft: &record.DraftRef{ID: 7, Rev: 3}},
		}, &got)
	if !slices.Equal(got.Choice, []string{"Wait"}) || len(got.Confirm) != 1 || !got.Pins {
		t.Errorf("choice %q, confirm %q, pins %v: want the unpinned answers only", got.Choice, got.Confirm, got.Pins)
	}
}
