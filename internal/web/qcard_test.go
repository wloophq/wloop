package web

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/domain/record"
)

// TestATasksQuestionStandsForItsPinnedDrafts draws a task whose open question pins its two drafts as one decision:
// the question card, each option with its pinned draft under it, and no card of either draft with buttons of its
// own; a draft no question pins keeps its card, and a pinned draft's own task sends the person to the question.
func TestATasksQuestionStandsForItsPinnedDrafts(t *testing.T) {
	var got struct {
		Gates, Drafts, Pins, Buttons []string
		Own                          []string
	}
	approve, comment, free := reviewDraft(), reviewDraft(), pendingDraft()
	approve.ID, approve.Gate, approve.PinnedBy = 42, 142, 3
	comment.ID, comment.Gate, comment.PinnedBy = 43, 143, 3
	free.ID, free.Gate = 44, 144
	q := api.Message{ID: 3, Unit: prUnit, Kind: "question", Text: "Approve, or comment?", Recommend: "approve", Pinned: []api.Draft{approve, comment},
		Options: []record.Option{{ID: "approve", Label: "Approve", Draft: &record.DraftRef{ID: 42, Rev: 3}}, {ID: "comment", Label: "Comment", Draft: &record.DraftRef{ID: 43, Rev: 3}}}}
	subject, _ := json.Marshal(q)
	gates := []api.GateView{{ID: 3, Task: "t-1", Kind: "question", Name: "question", Status: "open", Subject: subject,
		Intents: []api.IntentView{{ID: "answer", Label: "Answer", Answer: true}}}}
	for _, d := range []api.Draft{approve, comment, free} {
		g := api.DraftGate(d)
		g.Status, g.Intents = "open", []api.IntentView{{ID: "approve", Label: "Approve"}}
		gates = append(gates, g)
	}
	own := approve
	own.Task = "t-1"
	runGate(t, `
const e = { id: "t-1", title: "Review acme/api#7", gates: input.gates, verbs: [] };
taskState.id = "t-1"; taskState.drafts = [input.approve, input.comment, input.free];
const box = taskGates(e);
const ownTask = { id: "draft:42", title: "draft", gates: [{ kind: "draft", status: "open", subject: input.own }], verbs: [] };
done({ Gates: cls(box, "d-gate").map((x) => attr(x, "data-gate")), Drafts: cls(box, "d-draft").map((x) => attr(x, "data-draft")),
  Pins: cls(box, "d-pin").map((x) => attr(x, "data-draft")),
  Buttons: walk(box, (x) => x.tag === "button").map((b) => attr(b, "data-focus")).filter((f) => /draft:4[23]/.test(f)),
  Own: cls(taskGates(ownTask), "d-note").map((x) => x.textContent) });`,
		map[string]any{"gates": gates, "approve": approve, "comment": comment, "free": free, "own": own}, &got)
	if !slices.Equal(got.Gates, []string{"3"}) || !slices.Equal(got.Pins, []string{"42", "43"}) {
		t.Errorf("gate cards %q with pins %q, want the question alone, each option with its draft", got.Gates, got.Pins)
	}
	if !slices.Equal(got.Drafts, []string{"44"}) || len(got.Buttons) != 0 {
		t.Errorf("draft cards %q, buttons of the pinned drafts %q: want only the unpinned draft's card", got.Drafts, got.Buttons)
	}
	if len(got.Own) != 1 {
		t.Errorf("the pinned draft's own task says %q, want the note that its question decides it", got.Own)
	}
}

// TestASentDraftsDetailShowsItsPost draws a sent draft's own task under its state's heading, with no action, the
// line that it was posted linked to the post, why it was proposed labeled as such, its unit by its label, and an
// inline comment's Markdown body rendered.
func TestASentDraftsDetailShowsItsPost(t *testing.T) {
	var got struct {
		Heading, Posted, Href, Why, Where string
		Buttons                           int
		Strong                            []string
	}
	d := reviewDraft()
	d.State, d.Task, d.Gate, d.Intents, d.Where, d.Target, d.Label = "sent", "t-1", 0, nil, "", "", "acme/api#7"
	d.Unit, d.Permalink, d.Why = "github/acme/api#7", "https://example.test/acme/api/pull/7#review-1", "REQUEST_CHANGES held for the user"
	d.UpdatedAt = d.CreatedAt
	d.Fields[1].Fields[0].Fields[3].Value, d.Fields[1].Fields[0].Fields[3].Markdown = "Return the **error**.", true
	own := d
	runGate(t, `
const e = { id: "draft:7", title: "draft", gates: [{ kind: "draft", status: "approved", subject: input.own }], verbs: [{ verb: "approve", enabled: false, reason: "sent" }, { verb: "edit", enabled: false, reason: "sent" }] };
taskState.id = "draft:7"; taskState.drafts = [];
const box = taskGates(e);
const posted = cls(box, "d-posted")[0];
const link = walk(posted, (x) => x.tag === "a")[0];
done({ Heading: cls(box, "d-sub")[0].textContent, Buttons: walk(box, (x) => x.tag === "button").length, Posted: posted.textContent,
  Href: link ? attr(link, "href") : "", Why: cls(box, "why").map((x) => x.textContent).join(""), Where: cls(box, "ref")[0].textContent,
  Strong: walk(box, (x) => x.tag === "strong").map((x) => x.textContent) });`, map[string]any{"own": own}, &got)
	if got.Heading != "Sent" {
		t.Errorf("heading %q, want the sent state's word", got.Heading)
	}
	if got.Buttons != 0 || got.Href != d.Permalink || got.Posted == "" {
		t.Errorf("%d buttons, posted %q linked to %q: want no action and the post's link", got.Buttons, got.Posted, got.Href)
	}
	if got.Why != "Why it was proposed: REQUEST_CHANGES held for the user" {
		t.Errorf("why %q, want it labeled as the proposal's reason", got.Why)
	}
	if got.Where != "acme/api#7" {
		t.Errorf("where %q, want the unit's label", got.Where)
	}
	if !slices.Equal(got.Strong, []string{"error"}) {
		t.Errorf("strong %q, want the comment's Markdown rendered", got.Strong)
	}
}
