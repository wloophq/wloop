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
const log = { gets: [], posts: [], opened: [], closed: 0, renders: 0 };
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
  close: () => { log.closed++; },
  render: () => { log.renders++; },
};
const nodes = (st) => renderOmnibox(st, env);
const groups = (st) => all(nodes(st), (x) => x.attrs["data-group"]).map((g) => text(g.children[0]));
`

func omniPrelude(t *testing.T) string { return viewsPrelude(t) + staticJS(t, "omnibox.js") + omniHarness }

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
process.stdout.write(JSON.stringify({ Groups: groups(st), Links: all(n, (x) => x.tag === "a").map((a) => a.attrs.href), Views: text(byClass(n, "views")), Badge: text(byClass(n, "omni-badge")) }));
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
all(n, (x) => x.tag === "button")[1].attrs.onclick();
process.stdout.write(JSON.stringify({ Items: all(n, (x) => x.tag === "button").map(text), Ran: ran }));`, nil, &got)
	if !slices.Equal(got.Items, []string{"to Nextx", "donee"}) || !slices.Equal(got.Ran, []string{"done"}) {
		t.Fatalf("%+v", got)
	}
}

func TestOmniboxPanelMode(t *testing.T) {
	var got struct {
		Class   string
		Closed  int
		Shade   bool
		Focused bool
	}
	main := `
let closed = 0;
window.close = () => { closed++; };
const st = showOmnibox({ panel: true });
const box = document.body.children[0];
const field = box.children[0];
field.on.keydown({ key: "Escape", preventDefault() {} });
process.stdout.write(JSON.stringify({ Class: box.attrs.class, Closed: closed, Shade: String(box.attrs.class).includes("pal-back") }));`
	nodeScript(t, domShim+staticJS(t, "i18n.js")+jsFunction(t, appJS(t), "h")+jsConst(t, appJS(t), "API_VERSION")+strings.Replace(staticJS(t, "omnibox.js"), `"use strict";`, "", 1)+main, nil, &got)
	if !strings.Contains(got.Class, "omni-panel") || got.Shade || got.Closed != 1 {
		t.Fatalf("%+v: the panel is the omnibox alone, with no backdrop, and Esc calls window.close", got)
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
