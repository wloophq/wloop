package web

import (
	"slices"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/api"
)

// outboxFixture is an Outbox of two drafts of fake-nudge, one on a task whose ask the draft answers, and one
// written by hand.
func outboxFixture() api.OutboxPage {
	first, second, own := pendingDraft(), pendingDraft(), pendingDraft()
	second.ID, second.Text = 8, "Still waiting on the upload fix?"
	own.ID, own.Text, own.Origin = 9, "Thanks!", api.DraftOrigin{Kind: "user"}
	ctx := api.DraftContext{Asks: []api.AskView{{Item: "i1", Summary: "Review the retry change", Asker: "dana", Quotes: []string{"can you look?"}}},
		Items: []api.OutboxItem{{ID: "i1", Summary: "Review the retry change", State: "open"}}}
	return api.OutboxPage{Groups: []api.OutboxGroup{
		{Workflow: "fake-nudge", Title: "Ask who hasn't answered", About: "Drafted when a conversation where you asked something goes quiet.",
			Drafts: []api.OutboxDraft{{Draft: first, Context: ctx}, {Draft: second, Context: api.DraftContext{Asks: []api.AskView{}, Items: []api.OutboxItem{}}}}},
		{Title: "Written by hand", Drafts: []api.OutboxDraft{{Draft: own}}},
	}}
}

// outboxHarness draws the Outbox with the strings' plural of a draft count.
const outboxHarness = `
I18N.strings["{n} draft"] = { one: "{n} draft", other: "{n} drafts" };
globalThis.confirm = () => { throw new Error("the Outbox asked a browser confirm"); };
const box = () => outboxPage(input.page);
`

func TestTheOutboxRendersOneCardPerDraftUnderItsGroup(t *testing.T) {
	var got struct {
		Order   []string
		Quotes  []string
		Items   []string
		Context []int
	}
	runGate(t, outboxHarness+`
const marks = ["ob-title", "ob-n", "ob-about", "ob-workflow", "d-draft"];
const order = walk(box(), (x) => marks.some((m) => String(x.attrs.class || "").split(" ").includes(m)))
  .map((x) => x.attrs["data-draft"] ? "card " + x.attrs["data-draft"] : x.textContent);
const cards = cls(box(), "d-draft");
done({ Order: order, Quotes: cls(box(), "quote").map((q) => q.textContent), Items: cls(box(), "item").map((i) => i.textContent),
  Context: cards.map((c) => cls(c, "d-context").length) });`, map[string]any{"page": outboxFixture()}, &got)
	want := []string{"Ask who hasn't answered", "2 drafts", "fake-nudge", "Drafted when a conversation where you asked something goes quiet.", "card 7", "card 8",
		"Written by hand", "1 draft", "card 9"}
	if !slices.Equal(got.Order, want) {
		t.Errorf("the Outbox reads\n %q\nwant\n %q", got.Order, want)
	}
	if !slices.Equal(got.Quotes, []string{"can you look?"}) || !slices.Equal(got.Items, []string{"open Review the retry change"}) {
		t.Errorf("context quotes %q items %q, want the ask's quote and the item with its state", got.Quotes, got.Items)
	}
	if !slices.Equal(got.Context, []int{1, 0, 0}) {
		t.Errorf("context blocks per card %v, want one on the card that has context only", got.Context)
	}
}

func TestStopDraftingTheseDisablesTheWorkflow(t *testing.T) {
	var got struct {
		Before, Confirming, Cancelled []string
		Asked, Posts                  []any
	}
	runGate(t, outboxHarness+`
const buttons = (n) => cls(n, "ob-stop").flatMap((s) => walk(s, (x) => x.tag === "button").map((b) => b.attrs["data-focus"]));
const before = buttons(box());
await focus(box(), "outbox:stop:fake-nudge").on.click();
const confirming = buttons(box());
const asked = posts.slice();
await focus(box(), "outbox:stop-cancel:fake-nudge").on.click();
const cancelled = buttons(box());
await focus(box(), "outbox:stop:fake-nudge").on.click();
await focus(box(), "outbox:stop-confirm:fake-nudge").on.click();
done({ Before: before, Confirming: confirming, Cancelled: cancelled, Asked: asked, Posts: posts });`, map[string]any{"page": outboxFixture()}, &got)
	if !slices.Equal(got.Before, []string{"outbox:stop:fake-nudge"}) || !slices.Equal(got.Cancelled, got.Before) {
		t.Errorf("stop buttons %q, after cancel %q: want one, on the workflow's group only", got.Before, got.Cancelled)
	}
	if !slices.Equal(got.Confirming, []string{"outbox:stop-confirm:fake-nudge", "outbox:stop-cancel:fake-nudge"}) || len(got.Asked) != 0 {
		t.Errorf("after the first click %q, posted %v: want the inline confirm and nothing posted", got.Confirming, got.Asked)
	}
	want := []any{map[string]any{"path": "/api/workflows/fake-nudge/disable", "body": map[string]any{"source": "cockpit"}}}
	if mustJS(t, got.Posts) != mustJS(t, want) {
		t.Errorf("posts %v, want %v", got.Posts, want)
	}
}

func TestADraftLinksToItsTaskAndItsUnit(t *testing.T) {
	var got []string
	runGate(t, outboxHarness+`
const card = cls(box(), "d-draft")[0];
done(walk(card, (x) => x.tag === "a").map((a) => a.attrs.href));`, map[string]any{"page": outboxFixture()}, &got)
	if len(got) != 2 || !strings.HasSuffix(got[0], "task=t-1") || got[1] != "https://example.test/thread" {
		t.Errorf("links %q, want its task and its unit at the source", got)
	}
}

func TestTheViewLayoutPicksTheRenderer(t *testing.T) {
	var got struct {
		Outbox, Board, List []string
	}
	cat := servedViews(t)
	for i := range cat.Views {
		if cat.Views[i].ID == "needs-me" {
			cat.Views[i].Layout = "board"
		}
	}
	cat.Views = append(cat.Views, api.TaskViewSpec{ID: "send-later", Label: "Send later", Over: "gates", Layout: "outbox"})
	nodeRun(t, mixedPrelude(t), nil, `
I18N.strings["{n} draft"] = { one: "{n} draft", other: "{n} drafts" };
const classes = (nodes) => nodes.map((n) => String(n.attrs.class || ""));
outboxState.page = input.page;
tasksState.key = viewQueryKey("send-later", "", []);
const outbox = classes(viewPage({ ...route({}), view: "send-later" }, "").body);
tasksState.key = viewQueryKey("needs-me", "", []);
const board = classes(viewPage(route({}), "").body);
const list = classes(viewPage(route({ layout: "list" }), "").body);
process.stdout.write(JSON.stringify({ Outbox: outbox, Board: board, List: list }));`, mixedInput(t, map[string]any{"cat": cat, "page": outboxFixture()}), &got)
	if !slices.Equal(got.Outbox, []string{"outbox"}) {
		t.Errorf("a view whose layout is outbox draws %q, want the Outbox", got.Outbox)
	}
	if len(got.Board) != 1 || !strings.Contains(got.Board[0], "board") {
		t.Errorf("a view whose layout is board draws %q, want a board", got.Board)
	}
	if len(got.List) != 1 || strings.Contains(got.List[0], "board") {
		t.Errorf("the list picked on a board view draws %q, want the list", got.List)
	}
}
