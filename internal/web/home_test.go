package web

import (
	"slices"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/api"
)

// homeFixture is a home with a kind counter, a view counter, one sentence and a section of five tasks.
func homeFixture() api.Home {
	zero, one := 0, 1
	rows := []api.TaskView{}
	for _, id := range []string{"t-1", "t-2", "t-3"} {
		rows = append(rows, task(id, "next", nil))
	}
	return api.Home{
		Briefing: api.Briefing{Template: "default", Counters: []api.HomeCounter{
			{ID: "answer", Kind: "answer", View: "decide-answer", Count: 2, Label: "2 questions"},
			{ID: "loop", View: "in-loop", Count: 3, Label: "3 tasks"}},
			Sentences: [][]api.BriefingPart{{{Text: "The agent has "}, {Counter: &zero}, {Text: " for you and the loop handles "}, {Counter: &one}, {Text: "."}}}},
		Sections: []api.HomeSection{{ID: "blocked", View: "home-blocked", Label: "Blocked by you", Time: "owed", Rows: rows, Total: 5},
			{ID: "stalled", View: "home-stalled", Label: "Stalled the longest", Time: "moved", Rows: []api.TaskView{}, Total: 0}},
		Capture: api.HomeCapture{Hint: "Capture what's on your mind", Key: "n"},
	}
}

// homeRun renders input's home with a row per task that names its id, and runs main over the nodes in out.
const homeRun = `
const captured = [];
const env = { row: (e, view) => h("li", { "data-task": e.id, "data-view": view }, e.title), capture: async (title) => { captured.push(title); return true; } };
const nodes = renderHome(input, env);
const links = all(nodes, (x) => x.tag === "a").map((a) => ({ href: a.attrs.href, text: text(a) }));
`

func homePrelude(t *testing.T) string { return viewsPrelude(t) + staticJS(t, "home.js") }

func TestHomeRendersSentenceWithCounterLinks(t *testing.T) {
	var got struct {
		Sentence string
		Links    []struct{ Href, Text string }
	}
	nodeRun(t, homePrelude(t), nil, homeRun+`
process.stdout.write(JSON.stringify({ Sentence: text(byClass(nodes, "briefing")), Links: links }));`, homeFixture(), &got)
	if got.Sentence != "The agent has 2 questions for you and the loop handles 3 tasks. " {
		t.Errorf("sentence %q", got.Sentence)
	}
	if len(got.Links) < 2 || got.Links[0].Href != "#queue=answer" || got.Links[0].Text != "2 questions" {
		t.Errorf("a kind counter links %+v, want #queue=answer", got.Links)
	}
}

func TestHomeViewCounterLinksToView(t *testing.T) {
	var got struct{ Links []struct{ Href, Text string } }
	nodeRun(t, homePrelude(t), nil, homeRun+`process.stdout.write(JSON.stringify({ Links: links }));`, homeFixture(), &got)
	if len(got.Links) < 2 || got.Links[1].Href != "#view=in-loop" || got.Links[1].Text != "3 tasks" {
		t.Fatalf("a view counter links %+v", got.Links)
	}
}

func TestHomeSectionsShowAtMostTheirRows(t *testing.T) {
	var got struct {
		Rows    []string
		Views   []string
		Links   []struct{ Href, Text string }
		Stalled string
	}
	nodeRun(t, homePrelude(t), nil, homeRun+`
const blocked = all(nodes, (x) => x.attrs["data-section"] === "blocked")[0];
const stalled = all(nodes, (x) => x.attrs["data-section"] === "stalled")[0];
process.stdout.write(JSON.stringify({ Rows: all(blocked, (x) => x.attrs["data-task"]).map((x) => x.attrs["data-task"]),
  Views: all(blocked, (x) => x.attrs["data-view"]).map((x) => x.attrs["data-view"]), Links: links, Stalled: text(stalled) }));`, homeFixture(), &got)
	if !slices.Equal(got.Rows, []string{"t-1", "t-2", "t-3"}) || got.Views[0] != "home-blocked" {
		t.Errorf("rows %v in %v", got.Rows, got.Views)
	}
	if !slices.ContainsFunc(got.Links, func(l struct{ Href, Text string }) bool {
		return l.Href == "#view=home-blocked" && l.Text == "See all 5"
	}) {
		t.Errorf("no see-all link to home-blocked: %+v", got.Links)
	}
	if !strings.Contains(got.Stalled, "Nothing here right now.") || slices.ContainsFunc(got.Links, func(l struct{ Href, Text string }) bool { return l.Href == "#view=home-stalled" }) {
		t.Errorf("an empty section %q links %+v", got.Stalled, got.Links)
	}
}

func TestHomeCaptureCreatesTask(t *testing.T) {
	var got struct {
		Captured []string
		Value    string
		Hint     string
	}
	nodeRun(t, homePrelude(t), nil, homeRun+`
(async () => {
const field0 = all(nodes, (x) => x.tag === "input")[0];
const field = { value: "  Call Ada " };
await field0.attrs.onkeydown({ key: "Enter", target: field, preventDefault() {} });
const blank = { value: "   " };
await field0.attrs.onkeydown({ key: "Enter", target: blank, preventDefault() {} });
process.stdout.write(JSON.stringify({ Captured: captured, Value: field.value, Hint: field0.attrs.placeholder }));
})();`, homeFixture(), &got)
	if !slices.Equal(got.Captured, []string{"Call Ada"}) || got.Value != "" || got.Hint != "Capture what's on your mind" {
		t.Fatalf("%+v", got)
	}
}

func TestHomeStringsGoThroughT(t *testing.T) {
	var got struct{ Texts []string }
	nodeRun(t, homePrelude(t), nil, `
I18N.strings = new Proxy({}, { get: (_, k) => typeof k === "string" ? "«" + k + "»" : undefined, has: () => true });
`+homeRun+`
const chrome = [text(all(nodes, (x) => String(x.attrs.class || "").includes("see-all"))), text(byClass(nodes, "empty")), all(nodes, (x) => x.tag === "input")[0].attrs["aria-label"]];
process.stdout.write(JSON.stringify({ Texts: chrome }));`, homeFixture(), &got)
	for _, s := range got.Texts {
		if !strings.HasPrefix(s, "«") {
			t.Errorf("%q does not go through t", s)
		}
	}
}

func TestReadRouteDefaultsToHome(t *testing.T) {
	var got struct{ Empty, Task, Unit, View, Queue, Panel, Hash string }
	nodeRun(t, routesPrelude(t), nil, `
tasksState.catalog = input;
location.hash = "";
const home = readHash().view;
process.stdout.write(JSON.stringify({ Empty: readRoute("").view, Task: readRoute("#task=t-1").view, Unit: readRoute("#unit=github/acme%2Fapi%238").view,
  View: readRoute("#view=inbox").view, Queue: readRoute("#queue=answer").view, Panel: readRoute("#panel=omnibox").view, Hash: home }));`, viewsCatalog(), &got)
	if got.Empty != "home" || got.Hash != "home" {
		t.Errorf("an empty hash reads %q, readHash %q, want home", got.Empty, got.Hash)
	}
	if got.Task != "" || got.Unit != "" || got.View != "inbox" || got.Queue != "queue" || got.Panel != "" {
		t.Errorf("task %q unit %q view %q queue %q panel %q", got.Task, got.Unit, got.View, got.Queue, got.Panel)
	}
}

func TestReadRouteReadsQueueIds(t *testing.T) {
	var got struct {
		Queue, Q string
		IDs      []string
		Capped   int
	}
	nodeRun(t, routesPrelude(t), nil, `
const r = readRoute("#queue=send-draft&q=" + encodeURIComponent("where:general"));
const list = readRoute("#queue=list&ids=t-1,,t-2");
const many = readRoute("#queue=list&ids=" + Array.from({ length: 300 }, (_, i) => "t-" + i).join(","));
process.stdout.write(JSON.stringify({ Queue: r.queue, Q: r.q, IDs: list.ids, Capped: many.ids.length }));`, nil, &got)
	if got.Queue != "send-draft" || got.Q != "where:general" || !slices.Equal(got.IDs, []string{"t-1", "t-2"}) || got.Capped != 200 {
		t.Fatalf("%+v", got)
	}
}

func TestHomeSectionRowsAreNoListItemsInAList(t *testing.T) {
	var got struct{ Tags []string }
	nodeRun(t, homePrelude(t), nil, homeRun+`
process.stdout.write(JSON.stringify({ Tags: byClass(nodes, "rows").map((x) => x.tag) }));`, homeFixture(), &got)
	if len(got.Tags) == 0 || slices.Contains(got.Tags, "ol") {
		t.Fatalf("rows containers %v: a section's rows are divs, so they sit in a div, not a list", got.Tags)
	}
}

func TestAScreenRowWithNoViewUsesTheProfilesFirstView(t *testing.T) {
	var got struct{ Views []string }
	nodeRun(t, viewsPrelude(t)+jsFunction(t, appJS(t), "screenRow"), nil, `
tasksState.catalog = { views: [{ id: "mine", label: "Mine" }, { id: "later", label: "Later" }] };
const views = [];
const realLens = lensOf;
lensOf = (r) => { views.push(r.view); return realLens(r); };
screenRow({ id: "t-1", title: "one", units: [] });
screenRow({ id: "t-2", title: "two", units: [] }, "later");
process.stdout.write(JSON.stringify({ Views: views }));`, nil, &got)
	if !slices.Equal(got.Views, []string{"mine", "later"}) {
		t.Fatalf("lens views %v: a row no view names takes the profile's first view, never a view id the client names", got.Views)
	}
}
