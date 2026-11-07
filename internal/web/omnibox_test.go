package web

import (
	"slices"
	"strings"
	"testing"
)

// omniHarness drives the omnibox's state over a fake daemon: input.search answers GET /api/search by query,
// input.resolve POST /api/omnibox/resolve by text, input.ask the asks in order (a string throws it); posts
// are logged with their bodies; later runs at once unless held.
const omniHarness = `
const log = { gets: [], posts: [], opened: [], acts: [], closed: 0, renders: 0 };
const held = [];
let hold = false;
let asks = 0;
const env = {
  get: async (url) => { log.gets.push(url); return (input.search || {})[decodeURIComponent(url.split("q=")[1])] || { groups: [] }; },
  post: async (url, body) => {
    log.posts.push({ url, body: JSON.parse(JSON.stringify(body)) });
    if (url === "/api/omnibox/resolve") return (input.resolve || {})[body.text] || { kind: "search" };
    if (url === "/api/omnibox/ask") { const a = (input.ask || [])[asks++]; if (typeof a === "string") throw new Error(a); return a; }
    if (url === "/api/omnibox/run") return { results: body.operations.map(() => ({ ok: true, status: 200 })) };
    return {};
  },
  later: (fn) => { if (hold) held.push(fn); else fn(); return 1; },
  clear: () => {},
  now: () => 0,
  open: (href) => log.opened.push(href),
  act: async (u, id) => { log.acts.push(u.key + " " + id); },
  close: () => { log.closed++; },
  render: () => { log.renders++; },
};
const nodes = (st) => renderOmnibox(st, env);
const groups = (st) => all(nodes(st), (x) => x.attrs["data-group"]).map((g) => text(g.children[0]));
`

func omniPrelude(t *testing.T) string {
	return viewsPrelude(t) + staticJS(t, "omnibox.js") + omniHarness
}

func retriesSearch() map[string]any {
	res := func(id, title string, views ...string) map[string]any {
		return map[string]any{"task": map[string]any{"id": id, "title": title}, "views": views}
	}
	return map[string]any{
		"retries": map[string]any{"query": "retries", "total": 2, "groups": []any{
			map[string]any{"id": "you", "label": "Needs you", "count": 1, "results": []any{res("t-1", "add retries", "needs-me", "focus")}},
			map[string]any{"id": "others", "label": "Waiting on others", "count": 1, "results": []any{res("t-2", "retries review", "waiting")}}}},
		"re": map[string]any{"query": "re", "total": 1, "groups": []any{
			map[string]any{"id": "done", "label": "Done", "count": 9, "results": []any{res("t-9", "release")}}}},
	}
}

func TestOmniboxSearchGroups(t *testing.T) {
	var got struct {
		Groups []string
		Links  []string
		Views  string
		Badge  string
	}
	nodeRun(t, omniPrelude(t), nil, `
(async () => {
const st = omniboxNew(false, []);
await omniboxType(st, "retries", env);
const n = nodes(st);
process.stdout.write(JSON.stringify({ Groups: groups(st), Links: all(n, (x) => x.attrs.role === "option").map((o) => o.attrs["data-href"]), Views: text(byClass(n, "views")), Badge: text(byClass(n, "omni-badge")) }));
})();`, map[string]any{"search": retriesSearch()}, &got)
	if !slices.Equal(got.Groups, []string{"Needs you 1", "Waiting on others 1"}) || !slices.Equal(got.Links, []string{"#task=t-1", "#task=t-2"}) {
		t.Fatalf("%+v", got)
	}
	if !strings.Contains(got.Views, "needs-me · focus") || got.Badge != "instant" {
		t.Fatalf("views %q badge %q", got.Views, got.Badge)
	}
}

func TestOmniboxStaleReplyDropped(t *testing.T) {
	var got struct{ Groups []string }
	nodeRun(t, omniPrelude(t), nil, `
(async () => {
const st = omniboxNew(false, []);
hold = true;
const first = omniboxType(st, "re", env);
const second = omniboxType(st, "retries", env);
const [a, b] = held;
await b();
await a();
await Promise.all([first, second]);
process.stdout.write(JSON.stringify({ Groups: groups(st) }));
})();`, map[string]any{"search": retriesSearch()}, &got)
	if !slices.Equal(got.Groups, []string{"Needs you 1", "Waiting on others 1"}) {
		t.Fatalf("groups %v: the reply to the older text replaced the newer one", got.Groups)
	}
}

func TestOmniboxVerbPreviewRunsChecked(t *testing.T) {
	var got struct {
		Ran  []map[string]any
		Done []string
	}
	resolve := map[string]any{"revise acme/api#8 and acme/api#9": map[string]any{"kind": "verb", "preview": map[string]any{"operations": []any{
		map[string]any{"op": "action", "unit": map[string]string{"provider": "alpha", "key": "acme/api#8"}, "action": "review"},
		map[string]any{"op": "action", "unit": map[string]string{"provider": "alpha", "key": "acme/api#9"}, "action": "review"}}}}}
	nodeRun(t, omniPrelude(t), nil, `
(async () => {
const st = omniboxNew(false, []);
await omniboxType(st, "revise acme/api#8 and acme/api#9", env);
const boxes = all(nodes(st), (x) => x.tag === "input" && x.attrs.type === "checkbox");
boxes[1].attrs.onchange({ target: { checked: false } });
await omniboxEnter(st, env, false);
const run = log.posts.filter((p) => p.url === "/api/omnibox/run");
process.stdout.write(JSON.stringify({ Ran: run.map((p) => p.body.operations).flat(), Done: all(nodes(st), (x) => String(x.attrs.class || "") === "ok").map(text) }));
})();`, map[string]any{"resolve": resolve}, &got)
	if len(got.Ran) != 1 || got.Ran[0]["unit"].(map[string]any)["key"] != "acme/api#8" || len(got.Done) != 1 {
		t.Fatalf("%+v", got)
	}
}

func TestOmniboxAgentProposalEditAndConfirm(t *testing.T) {
	var got struct {
		Ran   []map[string]any
		Badge string
	}
	ask := []any{map[string]any{"proposal": map[string]any{"summary": "Start the retries work", "operations": []any{
		map[string]any{"op": "start", "brief": "add retries to the sender", "repo": "acme/api", "reason": "it is new work"}}}}}
	nodeRun(t, omniPrelude(t), nil, `
(async () => {
const st = omniboxNew(false, []);
await omniboxType(st, "start the retries work", env);
await omniboxEnter(st, env, true);
const badge = text(byClass(nodes(st), "omni-badge"));
const repo = all(nodes(st), (x) => x.tag === "input" && x.attrs["aria-label"] === "Repository")[0];
repo.attrs.oninput({ target: { value: "acme/web" } });
await omniboxEnter(st, env, false);
process.stdout.write(JSON.stringify({ Ran: log.posts.filter((p) => p.url === "/api/omnibox/run").map((p) => p.body.operations).flat(), Badge: badge }));
})();`, map[string]any{"ask": ask}, &got)
	if len(got.Ran) != 1 || got.Ran[0]["repo"] != "acme/web" || got.Badge != "agent" {
		t.Fatalf("%+v", got)
	}
}

func TestOmniboxAgentErrorKeepsSearch(t *testing.T) {
	var got struct {
		Groups []string
		Error  string
		Runs   int
	}
	nodeRun(t, omniPrelude(t), nil, `
(async () => {
const st = omniboxNew(false, []);
await omniboxType(st, "retries", env);
await omniboxEnter(st, env, true);
process.stdout.write(JSON.stringify({ Groups: groups(st), Error: text(byClass(nodes(st), "omni-ask-error")), Runs: log.posts.filter((p) => p.url === "/api/omnibox/run").length }));
})();`, map[string]any{"search": retriesSearch(), "ask": []any{"The agent could not answer: boom"}}, &got)
	if len(got.Groups) != 2 || got.Error != "The agent could not answer: boom" || got.Runs != 0 {
		t.Fatalf("%+v", got)
	}
}

func TestOmniboxFollowUpSendsHistory(t *testing.T) {
	var got struct{ Follow []map[string]string }
	ask := []any{map[string]any{"proposal": map[string]any{"summary": "Which one?", "question": "The sender or the receiver?", "operations": []any{}}},
		map[string]any{"answer": map[string]any{"text": "ok", "tasks": []any{}}}}
	nodeRun(t, omniPrelude(t), nil, `
(async () => {
const st = omniboxNew(false, []);
await omniboxType(st, "retries for the webhook", env);
await omniboxEnter(st, env, true);
const reply = all(nodes(st), (x) => x.tag === "input" && x.attrs["aria-label"] === "Your answer")[0];
reply.attrs.oninput({ target: { value: "both" } });
await omniboxFollow(st, env);
const asks = log.posts.filter((p) => p.url === "/api/omnibox/ask");
process.stdout.write(JSON.stringify({ Follow: asks[1].body.follow }));
})();`, map[string]any{"ask": ask}, &got)
	if len(got.Follow) != 1 || got.Follow[0]["question"] != "The sender or the receiver?" || got.Follow[0]["answer"] != "both" {
		t.Fatalf("%+v", got)
	}
}

func TestOmniboxAnswerOpensAsQueue(t *testing.T) {
	var got struct{ Hrefs []string }
	ask := []any{map[string]any{"answer": map[string]any{"text": "Two wait on Ada.", "tasks": []any{map[string]any{"id": "t-1", "title": "a"}, map[string]any{"id": "t-2", "title": "b"}}}}}
	nodeRun(t, omniPrelude(t), nil, `
(async () => {
const st = omniboxNew(false, []);
await omniboxType(st, "what waits on Ada", env);
await omniboxEnter(st, env, true);
process.stdout.write(JSON.stringify({ Hrefs: all(byClass(nodes(st), "omni-answer"), (x) => x.tag === "a").map((a) => a.attrs.href) }));
})();`, map[string]any{"ask": ask}, &got)
	if !slices.Equal(got.Hrefs, []string{"#task=t-1", "#task=t-2", "#queue=list&ids=t-1,t-2"}) {
		t.Fatalf("%v", got.Hrefs)
	}
}

func TestOmniboxStringsGoThroughT(t *testing.T) {
	var got struct{ Texts []string }
	resolve := map[string]any{"https://elsewhere.example/x": map[string]any{"kind": "search", "why": "unclaimed_address", "params": map[string]string{"address": "https://elsewhere.example/x"}},
		"api#5": map[string]any{"kind": "agent", "why": "ambiguous_ref", "params": map[string]string{"ref": "api#5"}}}
	nodeRun(t, omniPrelude(t), nil, `
I18N.strings = new Proxy({}, { get: (_, k) => typeof k === "string" ? "«" + k + "»" : undefined, has: () => true });
(async () => {
const st = omniboxNew(false, []);
await omniboxType(st, "https://elsewhere.example/x", env);
const why = text(byClass(nodes(st), "omni-why"));
const badge = text(byClass(nodes(st), "omni-badge"));
await omniboxType(st, "api#5", env);
const hint = text(byClass(nodes(st), "omni-hint"));
process.stdout.write(JSON.stringify({ Texts: [why, badge, hint] }));
})();`, map[string]any{"resolve": resolve}, &got)
	for _, s := range got.Texts {
		if !strings.HasPrefix(s, "«") {
			t.Errorf("%q does not go through t", s)
		}
	}
}

func TestOmniboxEmptyListsSelectionActions(t *testing.T) {
	var got struct {
		Items []string
		Ran   []string
	}
	nodeRun(t, omniPrelude(t), nil, `
const ran = [];
const st = omniboxNew(false, [{ label: "to Next", key: "x", run: () => ran.push("next") }, { label: "done", key: "e", run: () => ran.push("done") }]);
const n = nodes(st);
all(n, (x) => x.attrs.role === "option")[1].attrs.onclick();
process.stdout.write(JSON.stringify({ Items: all(n, (x) => x.attrs.role === "option").map(text), Ran: ran }));`, nil, &got)
	if !slices.Equal(got.Items, []string{"to Nextx", "donee"}) || !slices.Equal(got.Ran, []string{"done"}) {
		t.Fatalf("%+v", got)
	}
}

func pastedUnit(tasks ...any) map[string]any {
	return map[string]any{"https://example.test/acme/api/pull/8": map[string]any{"kind": "unit", "unit": map[string]any{
		"unit": map[string]string{"provider": "alpha", "key": "acme/api#8"}, "label": "acme/api#8", "tasks": tasks,
		"actions": []any{map[string]any{"id": "pause", "label": "Pause"}, map[string]any{"id": "remove", "label": "Remove"}}}}}
}

func TestOmniboxUnitActionUsesTheActionPath(t *testing.T) {
	var got struct {
		Acts []string
		Runs int
	}
	nodeRun(t, omniPrelude(t), nil, `
(async () => {
const st = omniboxNew(false, []);
await omniboxType(st, "https://example.test/acme/api/pull/8", env);
const pause = all(nodes(st), (x) => x.tag === "button" && text(x).includes("Pause"))[0];
await pause.attrs.onclick();
process.stdout.write(JSON.stringify({ Acts: log.acts, Runs: log.posts.filter((p) => p.url === "/api/omnibox/run").length }));
})();`, map[string]any{"resolve": pastedUnit()}, &got)
	if !slices.Equal(got.Acts, []string{"acme/api#8 pause"}) || got.Runs != 0 {
		t.Fatalf("%+v: a unit's action runs through the detail's action path, never /api/omnibox/run", got)
	}
}

func TestOmniboxPanelUnitActionOpensTheUnit(t *testing.T) {
	var got struct {
		Opened []string
		Acts   []string
		Runs   int
	}
	nodeRun(t, omniPrelude(t), nil, `
(async () => {
const st = omniboxNew(true, []);
await omniboxType(st, "https://example.test/acme/api/pull/8", env);
const pause = all(nodes(st), (x) => x.tag === "button" && text(x).includes("Pause"))[0];
await pause.attrs.onclick();
process.stdout.write(JSON.stringify({ Opened: log.opened, Acts: log.acts, Runs: log.posts.filter((p) => p.url === "/api/omnibox/run").length }));
})();`, map[string]any{"resolve": pastedUnit()}, &got)
	if !slices.Equal(got.Opened, []string{"#unit=alpha/acme%2Fapi%238"}) || len(got.Acts) != 0 || got.Runs != 0 {
		t.Fatalf("%+v: in the panel a unit's action opens the unit in the main window and runs nothing", got)
	}
}

func TestOmniboxActRunsTheServedRowsAction(t *testing.T) {
	var got struct {
		Got  []string
		Acts []string
	}
	nodeRun(t, omniPrelude(t), nil, `
(async () => {
state.specs = { byId: {} };
const gets = [];
const acts = [];
globalThis.getJSON = async (url) => { gets.push(url); return { row: { unit: { provider: "alpha", key: "acme/api#8" }, actions: [{ id: "pause", enabled: true, confirm: "Pause it?" }] } }; };
globalThis.refreshSpecs = async () => {};
globalThis.act = async (row, id, extra) => { acts.push(row.actions[0].confirm + " " + id + " " + JSON.stringify(extra)); return true; };
await omniboxAct({ provider: "alpha", key: "acme/api#8" }, "pause");
process.stdout.write(JSON.stringify({ Got: gets, Acts: acts }));
})();`, nil, &got)
	if !slices.Equal(got.Got, []string{"/api/units/alpha/acme%2Fapi%238"}) || !slices.Equal(got.Acts, []string{"Pause it? pause {}"}) {
		t.Fatalf("%+v: the action runs through act on the row the unit's detail serves, with its confirm", got)
	}
}

func TestOmniboxEnterOnAUnitRunsNothing(t *testing.T) {
	var got struct {
		Opened []string
		Acts   []string
		Runs   int
	}
	nodeRun(t, omniPrelude(t), nil, `
(async () => {
const st = omniboxNew(false, []);
await omniboxType(st, "https://example.test/acme/api/pull/8", env);
await omniboxEnter(st, env, false);
const bare = omniboxNew(false, []);
input.resolve["https://example.test/acme/api/pull/8"].unit.tasks = [];
await omniboxType(bare, "https://example.test/acme/api/pull/8", env);
await omniboxEnter(bare, env, false);
process.stdout.write(JSON.stringify({ Opened: log.opened, Acts: log.acts, Runs: log.posts.filter((p) => p.url === "/api/omnibox/run").length }));
})();`, map[string]any{"resolve": pastedUnit(map[string]any{"id": "t-8", "title": "review 8"})}, &got)
	if !slices.Equal(got.Opened, []string{"#task=t-8", "#unit=alpha/acme%2Fapi%238"}) || len(got.Acts) != 0 || got.Runs != 0 {
		t.Fatalf("%+v: Enter on a unit opens its task, else the unit, and runs no action", got)
	}
}

func TestOmniboxAskAgainAfterTypingDuringAnAsk(t *testing.T) {
	var got struct {
		Asks   int
		Asking bool
		Answer string
	}
	ask := []any{map[string]any{"answer": map[string]any{"text": "old", "tasks": []any{}}}, map[string]any{"answer": map[string]any{"text": "new", "tasks": []any{}}}}
	nodeRun(t, omniPrelude(t), nil, `
(async () => {
const st = omniboxNew(false, []);
await omniboxType(st, "who waits", env);
const first = omniboxEnter(st, env, true);
await omniboxType(st, "who waits on Ada", env);
await first;
await omniboxEnter(st, env, true);
process.stdout.write(JSON.stringify({ Asks: log.posts.filter((p) => p.url === "/api/omnibox/ask").length, Asking: st.asking, Answer: st.answer ? st.answer.answer.text : "" }));
})();`, map[string]any{"ask": ask}, &got)
	if got.Asks != 2 || got.Asking || got.Answer != "new" {
		t.Fatalf("%+v: typing during an ask lets the next ask run", got)
	}
}

func TestOmniboxPanelMode(t *testing.T) {
	var got struct {
		Class   string
		Closed  int
		Shade   bool
		Focused bool
		Modal   string
		Live    int
	}
	main := `
let closed = 0;
window.close = () => { closed++; };
const st = showOmnibox({ panel: true });
const box = document.body.children[0];
box.on.keydown({ key: "Escape", preventDefault() {}, stopPropagation() {} });
process.stdout.write(JSON.stringify({ Class: box.attrs.class, Closed: closed, Shade: String(box.attrs.class).includes("pal-back"), Modal: box.attrs["aria-modal"],
  Live: box.children.filter((c) => c.attrs && c.attrs["aria-live"]).length }));`
	nodeScript(t, domShim+staticJS(t, "i18n.js")+jsFunction(t, appJS(t), "h")+jsConst(t, appJS(t), "API_VERSION")+strings.Replace(staticJS(t, "omnibox.js"), `"use strict";`, "", 1)+main, nil, &got)
	if !strings.Contains(got.Class, "omni-panel") || got.Shade || got.Closed != 1 || got.Modal != "true" || got.Live != 0 {
		t.Fatalf("%+v: the panel is the omnibox alone, modal, with no backdrop, and Esc anywhere in it calls window.close", got)
	}
}

func TestOmniboxNeverOffersMerge(t *testing.T) {
	src := staticJS(t, "omnibox.js")
	for _, word := range []string{`"merge"`, `'merge'`} {
		if strings.Contains(src, word) {
			t.Errorf("omnibox.js names %s", word)
		}
	}
}

// omniSelected is the choice the rendered omnibox marks selected, by its data-choice, and how many it marks.
const omniSelected = `
const selected = (st) => all(nodes(st), (x) => x.attrs["aria-selected"] === "true" || x.attrs["data-selected"] === "true").map((x) => x.attrs["data-choice"]);
const press = (st, key, extra) => omniboxKey(st, env, { key, preventDefault() {}, stopPropagation() {}, ...(extra || {}) }, () => log.closed++);
`

func TestOmniboxArrowsMoveTheSelection(t *testing.T) {
	var got struct {
		First, Down, Past, Up, CtrlN, CtrlP []string
		Options                             int
		Handled                             bool
	}
	nodeRun(t, omniPrelude(t)+omniSelected, nil, `
(async () => {
const st = omniboxNew(false, []);
await omniboxType(st, "retries", env);
const out = { First: selected(st), Options: all(nodes(st), (x) => x.attrs.role === "option").length };
out.Handled = press(st, "ArrowDown");
out.Down = selected(st);
press(st, "ArrowDown");
out.Past = selected(st);
press(st, "ArrowUp");
out.Up = selected(st);
press(st, "n", { ctrlKey: true });
out.CtrlN = selected(st);
press(st, "p", { ctrlKey: true });
out.CtrlP = selected(st);
process.stdout.write(JSON.stringify(out));
})();`, map[string]any{"search": retriesSearch()}, &got)
	want := map[string][]string{"First": {"task:t-1"}, "Down": {"task:t-2"}, "Past": {"task:t-2"}, "Up": {"task:t-1"}, "CtrlN": {"task:t-2"}, "CtrlP": {"task:t-1"}}
	have := map[string][]string{"First": got.First, "Down": got.Down, "Past": got.Past, "Up": got.Up, "CtrlN": got.CtrlN, "CtrlP": got.CtrlP}
	for k, w := range want {
		if !slices.Equal(have[k], w) {
			t.Errorf("%s selects %v, want %v", k, have[k], w)
		}
	}
	if got.Options != 2 || !got.Handled {
		t.Errorf("%+v: every result is an option, and the arrows are the omnibox's", got)
	}
}

func TestOmniboxEnterOpensTheSelectedResult(t *testing.T) {
	var got struct {
		Opened []string
		Asks   int
	}
	nodeRun(t, omniPrelude(t)+omniSelected, nil, `
(async () => {
const st = omniboxNew(false, []);
await omniboxType(st, "retries", env);
press(st, "ArrowDown");
press(st, "Enter");
await new Promise((r) => setTimeout(r, 5));
press(st, "Enter", { metaKey: true });
await new Promise((r) => setTimeout(r, 5));
process.stdout.write(JSON.stringify({ Opened: log.opened, Asks: log.posts.filter((p) => p.url === "/api/omnibox/ask").length }));
})();`, map[string]any{"search": retriesSearch(), "ask": []any{map[string]any{"answer": map[string]any{"text": "none"}}}}, &got)
	if !slices.Equal(got.Opened, []string{"#task=t-2"}) || got.Asks != 1 {
		t.Fatalf("%+v: Enter opens the selected result, Cmd+Enter asks the agent", got)
	}
}

func TestOmniboxSelectionResetsOnANewQueryAndKeepsAcrossRedraws(t *testing.T) {
	var got struct{ Kept, Reordered, Reset []string }
	nodeRun(t, omniPrelude(t)+omniSelected, nil, `
(async () => {
const st = omniboxNew(false, []);
await omniboxType(st, "retries", env);
press(st, "ArrowDown");
const kept = selected(st);
st.search = { groups: [{ id: "others", label: "Waiting on others", count: 1, results: [{ task: { id: "t-2", title: "retries review" } }] },
  { id: "you", label: "Needs you", count: 1, results: [{ task: { id: "t-1", title: "add retries" } }] }] };
const reordered = selected(st);
await omniboxType(st, "re", env);
process.stdout.write(JSON.stringify({ Kept: kept, Reordered: reordered, Reset: selected(st) }));
})();`, map[string]any{"search": retriesSearch()}, &got)
	if !slices.Equal(got.Kept, []string{"task:t-2"}) || !slices.Equal(got.Reordered, []string{"task:t-2"}) || !slices.Equal(got.Reset, []string{"task:t-9"}) {
		t.Fatalf("%+v", got)
	}
}

func TestOmniboxEnterRunsTheSelectedUnit(t *testing.T) {
	var got struct {
		First  []string
		Opened []string
	}
	nodeRun(t, omniPrelude(t)+omniSelected, nil, `
(async () => {
const st = omniboxNew(false, []);
await omniboxType(st, "https://example.test/acme/api/pull/8", env);
const first = selected(st);
press(st, "Enter");
process.stdout.write(JSON.stringify({ First: first, Opened: log.opened }));
})();`, map[string]any{"resolve": pastedUnit()}, &got)
	if !slices.Equal(got.First, []string{"unit"}) || len(got.Opened) != 1 {
		t.Fatalf("%+v: a resolved unit is the first choice and Enter opens it", got)
	}
}

func TestOmniboxKeysHintIsWorded(t *testing.T) {
	var got struct{ Hint string }
	nodeRun(t, omniPrelude(t)+stringsPrelude(t, "pt-BR", "../../profiles/default/locales/pt-BR/strings.toml"), nil, `
process.stdout.write(JSON.stringify({ Hint: text(omniboxKeysHint()) }));`, nil, &got)
	for _, w := range []string{"para navegar", "para abrir", "para perguntar ao agente", "para fechar"} {
		if !strings.Contains(got.Hint, w) {
			t.Errorf("the keys hint %q lacks %q", got.Hint, w)
		}
	}
}

func TestOmniboxTabStaysInside(t *testing.T) {
	var got struct{ Wrapped, Back, Inside string }
	nodeRun(t, omniPrelude(t), nil, `
const stops = [{ name: "input" }, { name: "first" }, { name: "last" }];
const at = (el, shift) => { const to = omniboxTrap({ key: "Tab", shiftKey: !!shift }, stops, el); return to ? to.name : ""; };
process.stdout.write(JSON.stringify({ Wrapped: at(stops[2]), Back: at(stops[0], true), Inside: at(stops[1]) }));`, nil, &got)
	if got.Wrapped != "input" || got.Back != "last" || got.Inside != "" {
		t.Fatalf("%+v: Tab wraps inside the omnibox and leaves a move inside it alone", got)
	}
}

func TestOmniboxAnswerRendersMarkdown(t *testing.T) {
	var got struct{ Strong, Raw int }
	nodeRun(t, omniPrelude(t), nil, `
const st = omniboxNew(false, []);
st.text = "what waits";
st.answer = { answer: { text: "Two **tasks** wait", tasks: [] } };
const n = nodes(st);
process.stdout.write(JSON.stringify({ Strong: all(n, (x) => x.tag === "strong").length, Raw: text(n).includes("**") ? 1 : 0 }));`, nil, &got)
	if got.Strong != 1 || got.Raw != 0 {
		t.Fatalf("%+v: the agent's answer renders as markdown", got)
	}
}

func TestOmniboxRedrawKeepsTheFocusAndPointsAtTheSelection(t *testing.T) {
	var got struct {
		Focused, Active, Asked string
		Scrolled               bool
	}
	nodeRun(t, omniPrelude(t), nil, `
(async () => {
const st = omniboxNew(false, []);
await omniboxType(st, "retries", env);
const out = {};
const box = { focus() { out.Focused = "omni:op:1"; } };
const was = { getAttribute: () => "omni:op:1" };
globalThis.CSS = { escape: (s) => s };
globalThis.document = { activeElement: was, getElementById: (id) => ({ scrollIntoView() { out.Scrolled = id; } }) };
const results = { contains: (el) => el === was, replaceChildren() {}, querySelector: (sel) => { out.Asked = sel; return box; } };
const input = { attrs: {}, setAttribute(k, v) { this.attrs[k] = v; }, removeAttribute(k) { delete this.attrs[k]; } };
omniboxRedraw(st, env, input, results);
out.Active = input.attrs["aria-activedescendant"];
out.Scrolled = out.Scrolled === out.Active;
process.stdout.write(JSON.stringify(out));
})();`, map[string]any{"search": retriesSearch()}, &got)
	if got.Focused != "omni:op:1" || got.Asked != `[data-focus="omni:op:1"]` || got.Active != "omni-opt-0" || !got.Scrolled {
		t.Fatalf("%+v: a redraw gives the focus back to its control, and the field points at the selected choice, scrolled into view", got)
	}
}

func TestOmniboxEnterActsOnlyOnTheAnswerForTheCurrentText(t *testing.T) {
	var got struct {
		Before, After []string
	}
	nodeRun(t, omniPrelude(t)+omniSelected, nil, `
(async () => {
const st = omniboxNew(false, []);
await omniboxType(st, "re", env);
hold = true;
const typed = omniboxType(st, "retries", env);
const entered = omniboxEnter(st, env, false);
await new Promise((r) => setTimeout(r, 5));
const before = log.opened.slice();
hold = false;
held.splice(0).forEach((fn) => fn());
await typed;
await entered;
process.stdout.write(JSON.stringify({ Before: before, After: log.opened }));
})();`, map[string]any{"search": retriesSearch()}, &got)
	if len(got.Before) != 0 || !slices.Equal(got.After, []string{"#task=t-1"}) {
		t.Fatalf("%+v: Enter waits for the answer to the text typed, never runs the older text's first result", got)
	}
}

func TestTheOmniboxListboxHoldsOnlyOptions(t *testing.T) {
	var got struct {
		Outside, Controls, Lists int
		Status                   string
		Opened                   []string
	}
	resolve := map[string]any{"done t-1": map[string]any{"kind": "verb", "preview": map[string]any{"operations": []any{
		map[string]any{"op": "verb", "verb": "done", "task": "t-1"}}}}}
	search := map[string]any{"done t-1": retriesSearch()["retries"]}
	nodeRun(t, omniPrelude(t), nil, `
(async () => {
const st = omniboxNew(false, []);
await omniboxType(st, "done t-1", env);
const n = nodes(st);
const lists = all(n, (x) => x.attrs.role === "listbox");
const inList = new Set(all(lists, (x) => x.attrs.role === "option"));
const outside = all(n, (x) => x.attrs.role === "option").filter((o) => !inList.has(o)).length;
const controls = all(lists, (x) => ["button", "input", "a", "textarea"].includes(x.tag)).length;
all(lists, (x) => x.attrs.role === "option")[1].attrs.onclick();
process.stdout.write(JSON.stringify({ Outside: outside, Controls: controls, Lists: lists.length, Status: text(all(n, (x) => x.tag === "output")), Opened: log.opened }));
})();`, map[string]any{"resolve": resolve, "search": search}, &got)
	if got.Lists != 1 || got.Outside != 0 || got.Controls != 0 {
		t.Errorf("%+v: one listbox, holding every option and no other control", got)
	}
	if got.Status != "2 results" || !slices.Equal(got.Opened, []string{"#task=t-2"}) {
		t.Errorf("%+v: a polite status counts the results, and an option opens its task", got)
	}
}
