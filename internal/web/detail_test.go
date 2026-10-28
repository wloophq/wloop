package web

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/domain/card"
	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/record"
	"github.com/wloophq/wloop/internal/domain/unit"
)

var prUnit = unit.Unit{Provider: "github", Key: "acme/api#8"}

func questionTask() api.TaskView {
	return task("e-1", "inbox", func(e *api.TaskView) {
		e.Title, e.Changed, e.Category = "Pick a fix", true, "inbox"
		e.Props = map[string]json.RawMessage{"contexts": json.RawMessage(`["@decide"]`)}
		e.Units = []unit.Unit{prUnit}
		e.Asks = []api.AskView{{Item: "gh:review", Summary: "Review the retry change"}}
		subject, _ := json.Marshal(api.Message{ID: 3, Unit: prUnit, Kind: "question", Text: "Retry or revert?", Recommend: "b",
			Options: []record.Option{{ID: "a", Label: "Retry"}, {ID: "b", Label: "Revert"}}})
		e.Gates = []api.GateView{{ID: 3, Kind: "question", Status: "open", Intents: []api.IntentView{}, Subject: subject}}
		e.Cards = []api.CardView{{Card: card.Card{Unit: prUnit, Title: "Retry flaky uploads", State: "open", Why: "your review is requested",
			Link: "https://github.com/acme/api/pull/8", Signals: []card.Signal{{Key: "checks", Value: "failing", Tone: "bad"}},
			WaitsOn: []items.Person{{Key: "github:me", Name: "me"}}}}}
		e.Verbs = []api.VerbState{{Verb: "next", Enabled: true}, {Verb: "done", Enabled: true}, {Verb: "answer", Enabled: true}, {Verb: "seen", Enabled: true}, {Verb: "dismiss", Enabled: true}}
	})
}

// detailHarness records the pane's requests: getJSON answers from
// input.answers by URL, posts land in posts.
const detailHarness = `
const posts = [], asked = [];
detailEnv.getJSON = async (u) => { asked.push(u); if (!(u in input.answers)) throw new Error("404 " + u); return input.answers[u]; };
detailEnv.post = async (path, body) => { posts.push({ path, body }); return { task: {} }; };
detailEnv.notice = () => {}; detailEnv.changed = () => {}; detailEnv.render = () => {};
`

func TestUnitDetailRoute(t *testing.T) {
	mine := task("e-1", "next", func(e *api.TaskView) { e.Units = []unit.Unit{prUnit} })
	other := task("e-2", "next", func(e *api.TaskView) { e.Units = []unit.Unit{{Provider: "github", Key: "acme/api#80"}} })
	waiting := task("e-3", "waiting", func(e *api.TaskView) { e.Units = []unit.Unit{prUnit} })
	shipped := task("e-4", "done", func(e *api.TaskView) { e.Units, e.Category = []unit.Unit{prUnit}, "done" })
	page := func(es ...api.TaskView) api.TasksPage {
		return api.TasksPage{Full: true, Tasks: es, Removed: []string{}}
	}
	detail := map[string]any{"row": map[string]any{"unit": prUnit}, "events": []any{}, "items": []any{},
		"card": card.Snapshot{Card: card.Card{Unit: prUnit, Title: "Retry flaky uploads", State: "open"}}}
	var got struct {
		Text  string
		Tasks []string
		Route map[string]any
	}
	nodeRun(t, viewsPrelude(t)+detailHarness, nil, `
function hashString(x) { return routeHash(x.view, x.pr, new URLSearchParams(x.task ? { task: x.task } : {})); }
function readHash() { return { view: "next" }; }
tasksState.catalog = input.cat;
(async () => {
  const route = readRoute("#unit=github/acme%2Fapi%238");
  await loadDetailPane(route);
  const box = unitExtras(input.detail);
  process.stdout.write(JSON.stringify({ Text: text(box), Tasks: detailState.unitTasks.map((e) => e.id), Route: route }));
})();`, map[string]any{"detail": detail, "cat": viewsCatalog(), "answers": map[string]any{
		"/api/tasks?view=logbook": page(shipped),
		"/api/tasks?view=inbox":   page(),
		"/api/tasks?view=next":    page(mine, other),
		"/api/tasks?view=waiting": page(waiting),
		"/api/tasks?view=someday": page(),
	}}, &got)
	if got.Route["unit"] != "github:acme/api#8" {
		t.Fatalf("route %v", got.Route)
	}
	if !slices.Equal(got.Tasks, []string{"e-1", "e-3"}) {
		t.Errorf("the unit's tasks %v, want e-1 and e-3", got.Tasks)
	}
	for _, s := range []string{"Retry flaky uploads", "Tasks", "Task e-1", "Task e-3"} {
		if !strings.Contains(got.Text, s) {
			t.Errorf("the unit detail lacks %q: %s", s, got.Text)
		}
	}
	if strings.Contains(got.Text, "Task e-2") {
		t.Errorf("another unit's task shows: %s", got.Text)
	}
}

func TestOpeningClearsChanged(t *testing.T) {
	next := task("e-2", "next", func(e *api.TaskView) { e.Changed = true })
	var got struct{ Paths []string }
	nodeRun(t, viewsPrelude(t)+detailHarness, nil, `
(async () => {
  await loadDetailPane({ task: "e-1" });
  await loadDetailPane({ task: "e-1" });
  await loadDetailPane({ task: "e-2" });
  await loadDetailPane({ task: "" });
  await loadDetailPane({ task: "e-1" });
  process.stdout.write(JSON.stringify({ Paths: posts.map((p) => p.path + " " + JSON.stringify(p.body)) }));
})();`, map[string]any{"answers": map[string]any{"/api/tasks/e-1": questionTask(), "/api/tasks/e-2": next}}, &got)
	want := []string{`/api/tasks/e-1/seen {"source":"cockpit"}`, `/api/tasks/e-1/seen {"source":"cockpit"}`}
	if !slices.Equal(got.Paths, want) {
		t.Errorf("posts %v, want seen once per opening of the Inbox task: %v", got.Paths, want)
	}
}

func TestADraftOnATaskApprovesItsEditedText(t *testing.T) {
	e := task("draft:11", "inbox", func(e *api.TaskView) {
		e.Title = "Nudge the reviewer?"
		e.Units = []unit.Unit{prUnit}
		e.Gates = draftGates(api.Draft{ID: 11, Unit: "github/acme/api#8", Kind: "nudge", Target: "acme/api#8", Text: "Could you take a look?", State: "held", Rev: 2})
		e.Verbs = []api.VerbState{{Verb: "approve", Enabled: true}, {Verb: "dismiss", Enabled: true}, {Verb: "answer", Enabled: true}}
	})
	var got struct {
		Text   string
		Kinds  []int
		Posted []string
		Edited []string
		Keyed  []string
	}
	nodeRun(t, viewsPrelude(t)+detailHarness, nil, `
(async () => {
  await loadDetailPane({ task: "draft:11" });
  const pane = taskPage(detailState.task);
  posts.length = 0;
  await byFocus(pane, "detail:approve:draft:11").attrs.onclick();
  const posted = posts.map((p) => p.path);
  const show = (p) => p.path + " " + JSON.stringify(p.body);
  posts.length = 0;
  byFocus(pane, "detail:open:approve:draft:11").attrs.onclick();
  const editing = taskPage(detailState.task);
  byFocus(editing, "detail:draft-text:draft:11").attrs.oninput({ target: { value: "Could you review the retry change today?" } });
  await byFocus(taskPage(detailState.task), "detail:approve:draft:11").attrs.onclick();
  const edited = posts.map(show);
  posts.length = 0;
  byFocus(taskPage(detailState.task), "detail:open:approve:draft:11").attrs.onclick();
  byFocus(taskPage(detailState.task), "detail:draft-text:draft:11").attrs.oninput({ target: { value: "Ping on the retry change" } });
  await detailPane.act("approve", {});
  const keyed = posts.map(show);
  process.stdout.write(JSON.stringify({ Text: text(pane), Kinds: [byClass(pane, "d-gates").length, byClass(pane, "d-draft").length], Posted: posted, Edited: edited, Keyed: keyed }));
})();`, map[string]any{"answers": map[string]any{"/api/tasks/draft:11": e}}, &got)
	if !slices.Equal(got.Kinds, []int{1, 1}) {
		t.Errorf("gates sections and draft cards %v, want the draft among the gates", got.Kinds)
	}
	for _, s := range []string{"Could you take a look?", "acme/api#8"} {
		if !strings.Contains(got.Text, s) {
			t.Errorf("the detail lacks %q", s)
		}
	}
	if !slices.Equal(got.Posted, []string{"/api/tasks/draft:11/approve"}) {
		t.Errorf("approving the draft posted %v, want its task's approve", got.Posted)
	}
	if want := []string{`/api/tasks/draft:11/approve {"source":"cockpit","args":{"text":"Could you review the retry change today?","rev":"2"}}`}; !slices.Equal(got.Edited, want) {
		t.Errorf("approving the edited draft posted %v, want %v", got.Edited, want)
	}
	if want := []string{`/api/tasks/draft:11/approve {"source":"cockpit","args":{"text":"Ping on the retry change","rev":"2"}}`}; !slices.Equal(got.Keyed, want) {
		t.Errorf("the approve key during an edit posted %v, want %v", got.Keyed, want)
	}
}

func TestCardRefOpensTheUnitInItsPluginView(t *testing.T) {
	type link struct{ Href, Text, Target string }
	var got map[string]struct{ Ref, Ext []link }
	nodeRun(t, viewsPrelude(t), nil, `
const catalog = { views: [
  { id: "needs-me", provider: "github", rows: true }, { id: "dash", provider: "github", rows: false },
  { id: "all", provider: "github", rows: true }, { id: "slack", provider: "slack", rows: true },
] };
applyPlugins([{ id: "github", name: "GitHub" }, { id: "slack", name: "Slack" }]);
const links = (n, c) => byClass(n, c).map((a) => ({ Href: a.attrs.href, Text: text(a), Target: a.attrs.target || "" }));
const out = {};
for (const c of input) {
  const card = unitCard({ card: c });
  out[c.unit.provider] = { Ref: links(card, "ref"), Ext: links(card, "ext") };
}
process.stdout.write(JSON.stringify(out));`, []map[string]any{
		{"unit": map[string]string{"provider": "github", "key": "acme/api#8"}, "label": "acme/api#8", "link": "https://github.com/acme/api/pull/8"},
		{"unit": map[string]string{"provider": "slack", "key": "T1/C1/1700.1"}, "label": "#eng thread", "link": "slack://channel?id=C1"},
	}, &got)
	want := map[string]struct{ Ref, Ext []link }{
		"github": {Ref: []link{{Href: "#view=all&unit=github/acme%2Fapi%238", Text: "acme/api#8"}}, Ext: []link{{Href: "https://github.com/acme/api/pull/8", Text: "GitHub", Target: "_blank"}}},
		"slack":  {Ref: []link{{Href: "#view=slack&unit=slack/T1%2FC1%2F1700.1", Text: "#eng thread"}}, Ext: []link{{Href: "slack://channel?id=C1", Text: "Slack", Target: "_blank"}}},
	}
	for p, w := range want {
		g := got[p]
		if !slices.Equal(g.Ref, w.Ref) || !slices.Equal(g.Ext, w.Ext) {
			t.Errorf("%s card: ref %+v ext %+v, want ref %+v ext %+v", p, g.Ref, g.Ext, w.Ref, w.Ext)
		}
	}
}

func TestAVerbButtonSaysWhatItDoes(t *testing.T) {
	var got struct {
		On, Off string
	}
	e := task("e-2", "next", func(e *api.TaskView) {
		e.Verbs = []api.VerbState{{Verb: "done", Enabled: true}, {Verb: "someday", Reason: "not on a someday task"}}
	})
	cat := api.ViewsCatalog{Verbs: []api.VerbSpec{{Verb: "done", Label: "Finish", Summary: "Close it"}, {Verb: "someday", Label: "Someday", Summary: "Not now"}}}
	nodeRun(t, viewsPrelude(t), nil, `
tasksState.catalog = input.cat;
const pane = h("div", {}, ...verbButtons(input.task));
process.stdout.write(JSON.stringify({
  On: byFocus(pane, "detail:done:e-2").attrs.title,
  Off: byFocus(pane, "detail:someday:e-2").attrs.title,
}));`, map[string]any{"cat": cat, "task": e}, &got)
	if got.On != "Close it" || got.Off != "Not now\nnot on a someday task" {
		t.Errorf("titles %q and %q", got.On, got.Off)
	}
}

func TestAVerbAsksTheProfilesQuestion(t *testing.T) {
	var got struct{ Asked []string }
	e := task("e-3", "next", func(e *api.TaskView) { e.Verbs = []api.VerbState{{Verb: "waiting", Enabled: true}} })
	cat := api.ViewsCatalog{Verbs: []api.VerbSpec{{Verb: "waiting", Label: "Waiting", Summary: "Wait", Input: "On whom?"}}}
	nodeRun(t, viewsPrelude(t)+strings.Replace(staticJS(t, "keys.js"), `"use strict";`, "", 1)+detailHarness, nil, `
tasksState.catalog = input.cat;
detailEnv.ask = (label) => { asked.push(label); return "ana"; };
const pane = h("div", {}, ...verbButtons(input.task));
(async () => {
  await byFocus(pane, "detail:waiting:e-3").attrs.onclick();
  process.stdout.write(JSON.stringify({ Asked: asked }));
})();`, map[string]any{"cat": cat, "task": e}, &got)
	if len(got.Asked) != 1 || got.Asked[0] != "On whom?" {
		t.Errorf("asked %v", got.Asked)
	}
}

func TestAVerbTheTaskDoesNotOfferHasNoButton(t *testing.T) {
	var got struct{ Approve, Dismiss, Done int }
	e := task("e-4", "inbox", func(e *api.TaskView) { e.Verbs = []api.VerbState{{Verb: "done", Enabled: true}} })
	nodeRun(t, viewsPrelude(t)+detailHarness, nil, `
const pane = h("div", {}, ...taskPage(input.task));
const n = (verb) => byFocus(pane, "detail:" + verb + ":e-4") ? 1 : 0;
process.stdout.write(JSON.stringify({ Approve: n("approve"), Dismiss: n("dismiss"), Done: n("done") }));`, map[string]any{"task": e, "answers": map[string]any{}}, &got)
	if got.Approve != 0 || got.Dismiss != 0 || got.Done != 1 {
		t.Errorf("buttons %+v", got)
	}
}

func TestATaskOffersTheIntentsThatServeIt(t *testing.T) {
	e := task("e-3f9a", "inbox", func(e *api.TaskView) {
		e.Title, e.Props = "Add retries to the webhook sender", map[string]json.RawMessage{"kind": json.RawMessage(`"capture"`)}
		e.Actions = []api.ActionState{{ID: "start", Label: "Begin work", Enabled: true, Note: "Starts an agent session on the task's brief"}}
	})
	var got struct {
		Text  string
		Posts []struct {
			Path string
			Body map[string]any
		}
	}
	nodeRun(t, viewsPrelude(t)+detailHarness, nil, `
(async () => {
  await loadDetailPane({ task: "e-3f9a" });
  const pane = taskPage(detailState.task);
  await byFocus(pane, "detail:start:e-3f9a").attrs.onclick();
  process.stdout.write(JSON.stringify({ Text: text(pane), Posts: posts }));
})();`, map[string]any{"answers": map[string]any{"/api/tasks/e-3f9a": e}}, &got)
	if !strings.Contains(got.Text, "Begin work") {
		t.Errorf("the detail lacks the action's label: %s", got.Text)
	}
	last := len(got.Posts) - 1
	if last < 0 || got.Posts[last].Path != "/api/units/"+string(unit.Task)+"/e-3f9a/start" || got.Posts[last].Body["source"] != "cockpit" {
		t.Fatalf("posts %+v", got.Posts)
	}
}

// draftGates are the gates of a task carrying d.
func draftGates(d api.Draft) []api.GateView { return []api.GateView{api.DraftGate(d)} }
