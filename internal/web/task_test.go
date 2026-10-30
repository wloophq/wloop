package web

import (
	"encoding/json"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/domain/card"
	"github.com/wloophq/wloop/internal/domain/unit"
)

// taskCatalog is GET /api/views of a profile whose states, views and
// properties no code names.
func taskCatalog() api.ViewsCatalog {
	return api.ViewsCatalog{
		Views: []api.TaskViewSpec{{ID: "triage", Label: "Triage", Group: "task.category"}, {ID: "parked", Label: "Parked", Group: "task.state"},
			{ID: "walk", Label: "Weekly walk"}},
		Verbs: []api.VerbSpec{{Verb: "finish", Label: "Finish", Summary: "Close it"}, {Verb: "reply", Handler: "reply", Label: "Reply", Summary: "Hold a reply for approval"}},
		States: []api.StateSpec{{Name: "triage", Label: "To sort", Category: "inbox"}, {Name: "doing", Label: "Doing", Category: "active"},
			{Name: "parked", Label: "Parked", Category: "deferred"}, {Name: "later", Label: "Later", Category: "deferred"}, {Name: "shipped", Label: "Shipped", Category: "done"}},
		Props:  []api.PropSpec{{Name: "size", Type: "enum", Values: []string{"s", "m", "l"}}, {Name: "owner", Type: "actor"}},
		Review: []string{"triage", "parked"},
	}
}

func raw(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// pageTask is a task with every section of the page filled.
func pageTask(t *testing.T) api.TaskView {
	at := time.Date(2026, 10, 27, 14, 0, 0, 0, time.UTC)
	return task("t-0123456789abcdef", "doing", func(e *api.TaskView) {
		e.Category, e.Title, e.OwedBy = "active", "Ship the retry change", "me"
		e.Units = []unit.Unit{prUnit}
		e.Cards = []api.CardView{{Card: card.Card{Unit: prUnit, Label: "acme/api#8", Link: "https://github.com/acme/api/pull/8", Title: "Retry flaky uploads"}}}
		e.Props = map[string]json.RawMessage{"size": raw(t, "m"), "owner": raw(t, map[string]string{"key": "github:ana", "name": "Ana"})}
		e.Asks = []api.AskView{{Item: "slack:1", Summary: "Ship it before Friday", Asker: "Bruno", Permalink: "https://chat.example/p/1",
			Quotes: []string{"can we get the retry out before the freeze?", "Friday at the latest"}}}
		e.Brief = &api.RecordView{Kind: "brief", At: at, Data: raw(t, map[string]any{"goal": "Retry uploads that fail on a timeout", "risk": "medium"})}
		e.Gates = []api.GateView{
			{ID: 7, Task: e.ID, Kind: "plan", Name: "plan", Status: "open", Text: "Run this?", Intents: []api.IntentView{{ID: "run", Label: "Run now"}, {ID: "skip", Label: "Skip"}}},
			{ID: 9, Task: e.ID, Kind: "question", Name: "question", Status: "open", Text: "The agent asks",
				Intents: []api.IntentView{{ID: "answer", Label: "Answer", Answer: true}, {ID: "dismiss", Label: "Dismiss"}}},
		}
		e.Runs = []api.RunView{
			{Seq: 3, Intent: "start", State: "running", Manager: "orca", Worktree: "/work/acme-api-retry", Session: "s-1", SessionState: "live", Actions: []string{"open", "enter", "transcript"}},
			{Seq: 2, Intent: "brief", State: "done", Actions: []string{"transcript"}},
		}
		e.Verbs = []api.VerbState{{Verb: "finish", Enabled: true}, {Verb: "reply", Enabled: true}}
		e.Children = []string{"t-00000000000000c1"}
	})
}

func timelineEvents() []api.TimelineEvent {
	at := time.Date(2026, 10, 27, 15, 0, 0, 0, time.UTC)
	return []api.TimelineEvent{
		{Seq: 12, Kind: "message", Actor: "agent", At: at.Add(2 * time.Minute), Data: json.RawMessage(`{"text":"The retry change is ready for review."}`)},
		{Seq: 11, Kind: "verb", Sub: "start", Actor: "person", At: at.Add(time.Minute)},
		{Seq: 10, Kind: "message", Actor: "person", At: at, Data: json.RawMessage(`{"text":"Take the timeout case first."}`)},
	}
}

// taskPageHarness drives the page over domShim: page(id) loads the task and
// draws it into a box; walk, cls and focus read the drawn tree.
const taskPageHarness = `
const posts = [], asked = [], notices = [];
detailEnv.getJSON = async (u) => { asked.push(u); if (!(u in input.answers)) throw new Error("404 " + u); return input.answers[u]; };
detailEnv.post = async (path, body) => { posts.push({ path, body }); return {}; };
detailEnv.notice = (t, err) => notices.push((err ? "! " : "") + t);
detailEnv.changed = () => {};
detailEnv.render = () => {};
tasksState.catalog = input.catalog;
function walk(n, pred, out = []) {
  if (n && Array.isArray(n.children)) { if (pred(n)) out.push(n); n.children.forEach((c) => walk(c, pred, out)); }
  return out;
}
const cls = (n, c) => walk(n, (x) => String(x.attrs.class || "").split(" ").includes(c));
const focus = (n, f) => walk(n, (x) => x.attrs["data-focus"] === f)[0];
async function page(id) {
  if (!detailState.task || detailState.task.id !== id) await loadDetailPane({ task: id });
  const box = document.createElement("aside");
  renderTaskDetail(box, { task: id });
  return box;
}
const done = (v) => process.stdout.write(JSON.stringify(v));
`

func runTaskPage(t *testing.T, main string, answers map[string]any, out any) {
	t.Helper()
	runTaskPageOn(t, taskCatalog(), main, answers, out)
}

func runTaskPageOn(t *testing.T, cat api.ViewsCatalog, main string, answers map[string]any, out any) {
	t.Helper()
	nodeRun(t, cockpitScript(t)+taskPageHarness, nil, "(async () => {\n"+main+"\n})().catch((e) => { console.error(e); process.exit(1); });",
		map[string]any{"catalog": cat, "answers": answers}, out)
}

func pageAnswers(t *testing.T) map[string]any {
	e := pageTask(t)
	return map[string]any{
		"/api/tasks/" + e.ID:               e,
		"/api/tasks/" + e.ID + "/timeline": timelineEvents(),
	}
}

func TestTaskPageSections(t *testing.T) {
	var got struct {
		Sections []string
		Head     string
		State    string
		Category string
		Props    map[string]string
		Ref      string
		Ext      string
		Child    string
	}
	runTaskPage(t, `
const box = await page("t-0123456789abcdef");
const head = box.children[0];
done({
  Sections: box.children.map((c) => c.attrs.class),
  Head: head.textContent,
  State: cls(head, "state")[0].textContent,
  Category: cls(head, "state")[0].attrs["data-category"],
  Props: Object.fromEntries(walk(head, (x) => x.tag === "dd").map((d) => [d.attrs["data-prop"], d.textContent])),
  Ref: cls(head, "ref")[0].attrs.href,
  Ext: cls(head, "ext")[0].attrs.href,
  Child: focus(box, "child:t-00000000000000c1").attrs.href,
});`, pageAnswers(t), &got)
	want := []string{"d-head", "d-asks", "d-brief", "d-gates", "d-runs", "d-timeline", "d-compose", "d-children"}
	if !slices.Equal(got.Sections, want) {
		t.Errorf("sections %v, want %v", got.Sections, want)
	}
	if got.State != "Doing" || got.Category != "active" {
		t.Errorf("the state reads %q in category %q, want the catalog's Doing in active", got.State, got.Category)
	}
	if got.Props["size"] != "m" || got.Props["owner"] != "Ana" {
		t.Errorf("props %v", got.Props)
	}
	if got.Ref != "#view=all&unit=github/acme%2Fapi%238" || got.Ext != "https://github.com/acme/api/pull/8" {
		t.Errorf("links: ref %q ext %q", got.Ref, got.Ext)
	}
	for _, s := range []string{"Ship the retry change", "owed by me", "Retry flaky uploads", "Finish"} {
		if !strings.Contains(got.Head, s) {
			t.Errorf("the header lacks %q: %s", s, got.Head)
		}
	}
	if !strings.Contains(got.Child, "task=t-00000000000000c1") {
		t.Errorf("the child links to %q", got.Child)
	}
}

func TestAsksShowTheirSourceQuotes(t *testing.T) {
	var got struct {
		Quotes []string
		Ask    string
		Source string
	}
	runTaskPage(t, `
const box = await page("t-0123456789abcdef");
const asks = cls(box, "d-asks")[0];
done({ Quotes: cls(asks, "quote").map((q) => q.textContent), Ask: cls(asks, "ask")[0].textContent, Source: cls(asks, "ext")[0].attrs.href });`, pageAnswers(t), &got)
	if !slices.Equal(got.Quotes, []string{"can we get the retry out before the freeze?", "Friday at the latest"}) {
		t.Errorf("quotes %q", got.Quotes)
	}
	if !strings.Contains(got.Ask, "Bruno: Ship it before Friday") || got.Source != "https://chat.example/p/1" {
		t.Errorf("ask %q from %q", got.Ask, got.Source)
	}
}

func TestAGateShowsItsProfileIntentsAndDigitKeys(t *testing.T) {
	var got struct {
		Buttons [][]string
		Boxes   []int
		Keyed   []string
		Clicked []any
		Empty   string
	}
	src := strings.Replace(staticJS(t, "keys.js"), `if (typeof document !== "undefined" && typeof state !== "undefined") installKeys(appEnv());`, "", 1)
	raw, _ := json.Marshal(webBindings(t))
	runTaskPage(t, strings.Replace(src, `"use strict";`, "", 1)+`
const box = await page("t-0123456789abcdef");
const gates = cls(box, "d-gate");
const keys = createKeys({
  selected: () => detailState.task,
  questionAt: (el) => detailPane.gateAt(el),
  resolveGate: (task, gate, intent) => detailPane.resolve(task, gate, intent),
  verbs: () => [],
});
keys.setBindings(`+string(raw)+`);
const inGate = (g) => ({ tagName: "DIV", closest: (s) => (s === ".d-gate" ? g : null) });
const press = (key, target) => keys.handle({ key, code: "Digit" + key, target, defaultPrevented: false, preventDefault() {} });
await press("2", inGate(gates[0]));
await press("1", inGate(gates[1]));
const empty = notices.slice(-1)[0];
const keyed = posts.splice(0).map((p) => p.path);
focus(box, "gate:9:text").on.input({ target: { value: "Take the timeout case first" } });
await focus(box, "gate:9:answer").on.click();
done({
  Buttons: gates.map((g) => walk(g, (x) => x.tag === "button").map((b) => b.textContent)),
  Boxes: gates.map((g) => walk(g, (x) => x.tag === "textarea").length),
  Keyed: keyed, Clicked: posts, Empty: empty,
});`, pageAnswers(t), &got)
	if want := [][]string{{"1 Run now", "2 Skip"}, {"1 Answer", "2 Dismiss"}}; !slices.EqualFunc(got.Buttons, want, slices.Equal) {
		t.Errorf("gate buttons %q, want %q", got.Buttons, want)
	}
	if !slices.Equal(got.Boxes, []int{0, 1}) {
		t.Errorf("answer boxes %v, want one on the gate whose intent takes an answer", got.Boxes)
	}
	if !slices.Equal(got.Keyed, []string{"/api/gates/7/skip"}) || !strings.HasPrefix(got.Empty, "! Answer") {
		t.Errorf("digits posted %v, then said %q", got.Keyed, got.Empty)
	}
	want := []any{map[string]any{"path": "/api/gates/9/answer", "body": map[string]any{"source": "cockpit", "args": map[string]any{"guidance": "Take the timeout case first"}}}}
	if mustJS(t, got.Clicked) != mustJS(t, want) {
		t.Errorf("answering posted %v, want %v", got.Clicked, want)
	}
}

func TestARunOffersOpenEnterTranscript(t *testing.T) {
	var got struct {
		Labels     []string
		Off        []bool
		Posts      []string
		Asked      string
		Transcript []string
	}
	answers := pageAnswers(t)
	answers["/api/runs/3/transcript"] = []map[string]string{{"role": "user", "text": "Take the timeout case first."}, {"role": "assistant", "text": "Done; tests pass."}}
	runTaskPage(t, `
let box = await page("t-0123456789abcdef");
await focus(box, "run:3:open").on.click();
await focus(box, "run:3:enter").on.click();
await focus(box, "run:3:transcript").on.click();
const second = walk(cls(box, "d-run")[1], (x) => x.tag === "button");
box = await page("t-0123456789abcdef");
done({
  Labels: walk(cls(box, "d-run")[0], (x) => x.tag === "button").map((b) => b.textContent),
  Off: second.map((b) => "disabled" in b.attrs),
  Posts: posts.map((p) => p.path),
  Asked: asked.slice(-1)[0],
  Transcript: cls(box, "transcript").flatMap((o) => o.children.map((li) => li.textContent)),
});`, answers, &got)
	if !slices.Equal(got.Labels, []string{"Open workspace", "Enter session", "Transcript"}) {
		t.Errorf("run buttons %v", got.Labels)
	}
	if !slices.Equal(got.Off, []bool{true, true, false}) {
		t.Errorf("a run offering only its transcript: disabled %v", got.Off)
	}
	if !slices.Equal(got.Posts, []string{"/api/runs/3/open", "/api/runs/3/enter"}) || got.Asked != "/api/runs/3/transcript" {
		t.Errorf("posts %v, asked %q", got.Posts, got.Asked)
	}
	if !slices.Equal(got.Transcript, []string{"user: Take the timeout case first.", "assistant: Done; tests pass."}) {
		t.Errorf("transcript %q", got.Transcript)
	}
}

func TestTheTimelineFiltersByKind(t *testing.T) {
	var got struct {
		Before, After []string
		Chips         []string
		Asked         string
	}
	answers := pageAnswers(t)
	answers["/api/tasks/t-0123456789abcdef/timeline?kinds=message"] = timelineEvents()
	runTaskPage(t, `
let box = await page("t-0123456789abcdef");
const said = (b) => cls(b, "msg").map((m) => m.textContent);
const before = said(box);
const chips = walk(cls(box, "kinds")[0], (x) => x.tag === "button").map((b) => b.textContent);
await focus(box, "timeline:kind:message").on.click();
box = await page("t-0123456789abcdef");
done({ Before: before, After: said(box), Chips: chips, Asked: asked.slice(-1)[0] });`, answers, &got)
	if len(got.Before) != 3 || !strings.Contains(got.Before[0], "Take the timeout case first.") || !strings.Contains(got.Before[2], "ready for review") {
		t.Errorf("the chat, oldest first: %q", got.Before)
	}
	if !slices.Equal(got.Chips, []string{"all", "message", "verb"}) {
		t.Errorf("kind chips %v", got.Chips)
	}
	if got.Asked != "/api/tasks/t-0123456789abcdef/timeline?kinds=message" || len(got.After) != 2 {
		t.Errorf("filtered to messages: asked %q, shows %q", got.Asked, got.After)
	}
}

func TestComposerPostsToChat(t *testing.T) {
	var got struct {
		Buttons []string
		Primary string
		Posts   []any
		Empty   string
	}
	runTaskPage(t, `
let box = await page("t-0123456789abcdef");
const compose = cls(box, "d-compose")[0];
await focus(box, "task:compose:send").on.click();
const empty = notices.slice(-1)[0];
focus(box, "task:compose:text").on.input({ target: { value: "Take the timeout case first." } });
await focus(box, "task:compose:send").on.click();
box = await page("t-0123456789abcdef");
focus(box, "task:compose:text").on.input({ target: { value: "We ship Friday." } });
await focus(box, "task:compose:draft").on.click();
done({
  Buttons: walk(compose, (x) => x.tag === "button").map((b) => b.textContent),
  Primary: cls(compose, "primary")[0].textContent,
  Posts: posts, Empty: empty,
});`, pageAnswers(t), &got)
	if !slices.Equal(got.Buttons, []string{"Send", "Save as draft", "Send reply"}) || got.Primary != "Send" {
		t.Errorf("composer buttons %v, primary %q", got.Buttons, got.Primary)
	}
	want := []any{
		map[string]any{"path": "/api/tasks/t-0123456789abcdef/chat", "body": map[string]any{"text": "Take the timeout case first."}},
		map[string]any{"path": "/api/tasks/t-0123456789abcdef/reply", "body": map[string]any{"source": "cockpit", "args": map[string]any{"kind": "reply", "send": "draft", "text": "We ship Friday."}}},
	}
	if mustJS(t, got.Posts) != mustJS(t, want) {
		t.Errorf("posts %v, want %v", got.Posts, want)
	}
	if got.Empty != "! Write a message first" {
		t.Errorf("an empty composer said %q", got.Empty)
	}
}

// bundledViewIDs are the bundled profile's view ids, which no client logic may name.
var bundledViewIDs = []string{"inbox", "needs-me", "waiting", "snoozed", "someday", "focus", "projects", "logbook", "outbox"}

func TestGroupingReadsCategoryNotStateNames(t *testing.T) {
	tasks := []api.TaskView{
		task("t-1", "shipped", func(e *api.TaskView) { e.Category = "done" }),
		task("t-2", "later", func(e *api.TaskView) { e.Category = "deferred" }),
		task("t-3", "doing", func(e *api.TaskView) { e.Category = "active" }),
		task("t-4", "parked", func(e *api.TaskView) { e.Category = "deferred" }),
		task("t-5", "triage", func(e *api.TaskView) { e.Category = "inbox" }),
		task("t-6", "doing", func(e *api.TaskView) {
			e.Category = "active"
			e.Props = map[string]json.RawMessage{"size": json.RawMessage(`"l"`)}
		}),
	}
	type group struct {
		Label string   `json:"label"`
		IDs   []string `json:"ids"`
	}
	var got map[string][]group
	answers := map[string]any{"tasks": tasks}
	nodeRun(t, cockpitScript(t)+taskPageHarness, nil, `
const g = (group) => viewGroups({ id: "any", group }, input.answers.tasks).map((x) => ({ label: x.label, ids: x.tasks.map((e) => e.id) }));
done({ category: g("task.category"), state: g("task.state"), size: g("task.prop.size"), none: g("") });`,
		map[string]any{"catalog": taskCatalog(), "answers": answers}, &got)
	want := map[string][]group{
		"category": {{"inbox", []string{"t-5"}}, {"active", []string{"t-3", "t-6"}}, {"deferred", []string{"t-2", "t-4"}}, {"done", []string{"t-1"}}},
		"state":    {{"To sort", []string{"t-5"}}, {"Doing", []string{"t-3", "t-6"}}, {"Parked", []string{"t-4"}}, {"Later", []string{"t-2"}}, {"Shipped", []string{"t-1"}}},
		"size":     {{"l", []string{"t-6"}}, {"None", []string{"t-1", "t-2", "t-3", "t-4", "t-5"}}},
		"none":     {{"", []string{"t-1", "t-2", "t-3", "t-4", "t-5", "t-6"}}},
	}
	for k, w := range want {
		if !slices.EqualFunc(got[k], w, func(a, b group) bool { return a.Label == b.Label && slices.Equal(a.IDs, b.IDs) }) {
			t.Errorf("by %s: %+v, want %+v", k, got[k], w)
		}
	}
	ids := strings.Join(bundledViewIDs, "|")
	logic := regexp.MustCompile(`[!=]==?\s*"(` + ids + `)"|"(` + ids + `)"\s*[!=]==?|\(\s*"(` + ids + `)"\s*[,)]|[\[,]\s*"(` + ids + `)"\s*[,\]]|case\s+"(` + ids + `)"`)
	for _, f := range []string{"views.js", "review.js", "keys.js", "task.js"} {
		for _, m := range logic.FindAllString(staticJS(t, f), -1) {
			t.Errorf("%s uses a view id as logic: %s", f, m)
		}
	}
}

func TestReviewStepsAreViews(t *testing.T) {
	var got struct {
		Head  string
		Steps []string
		Gets  []string
		Posts []string
		Tasks []string
	}
	review := map[string]any{"id": "r-1", "started_at": "2026-10-22T12:00:00Z", "done": false, "steps": []any{
		map[string]any{"id": "sort", "label": "Sort what came in", "view": "triage", "count": 1, "done": false},
		map[string]any{"id": "parked", "label": "Look at what waits", "view": "parked", "count": 0, "done": true},
	}}
	nodeRun(t, cockpitScript(t)+taskPageHarness, nil, `
const gets = [], sent = [];
reviewEnv.getJSON = async (url) => { gets.push(url); return url === "/api/review" ? input.answers.review : { tasks: [input.answers.task] }; };
reviewEnv.post = async (path) => { sent.push(path); return input.answers.review; };
reviewEnv.notice = () => {}; reviewEnv.render = () => {}; reviewEnv.focusStep = () => {};
(async () => {
  const route = { view: REVIEW, area: "", ctx: [], q: "", task: "" };
  await loadTasks(route);
  renderTasks(route);
  const panel = document.getElementById("tasks");
  if (panel.hidden) throw new Error("the review mode is hidden");
  await reviewKey("done");
  done({ Head: cls(panel, "tasks-head")[0].textContent, Steps: cls(panel, "review-label").map((x) => x.textContent), Gets: gets, Posts: sent,
    Tasks: tasksState.tasks.map((e) => e.id) });
})();`, map[string]any{"catalog": taskCatalog(), "answers": map[string]any{"review": review, "task": task("t-1", "triage", nil)}}, &got)
	if got.Head != "Review" {
		t.Errorf("the review is titled %q, want the mode's own title", got.Head)
	}
	if !slices.Equal(got.Steps, []string{"Sort what came in", "Look at what waits"}) {
		t.Errorf("steps %v", got.Steps)
	}
	if !slices.Equal(got.Gets[:2], []string{"/api/review", "/api/tasks?view=triage"}) || !slices.Equal(got.Tasks, []string{"t-1"}) {
		t.Errorf("gets %v, tasks %v", got.Gets, got.Tasks)
	}
	if !slices.Equal(got.Posts, []string{"/api/review/steps/sort/done"}) {
		t.Errorf("posts %v", got.Posts)
	}
}

func TestComposerSendIsTheDefault(t *testing.T) {
	for _, send := range []string{"now", "draft"} {
		cat := taskCatalog()
		for i, v := range cat.Verbs {
			if v.Handler == "reply" {
				cat.Verbs[i].Verb, cat.Verbs[i].Send = "respond", send
			}
		}
		answers := pageAnswers(t)
		e := pageTask(t)
		e.Verbs = []api.VerbState{{Verb: "finish", Enabled: true}, {Verb: "respond", Enabled: true}}
		answers["/api/tasks/"+e.ID] = e
		var got struct {
			Primary string
			Replies []string
			Default string
			Posts   []any
		}
		runTaskPageOn(t, cat, `
let box = await page("t-0123456789abcdef");
const compose = cls(box, "d-compose")[0];
const buttons = walk(compose, (x) => x.tag === "button");
focus(box, "task:compose:text").on.input({ target: { value: "Take the timeout case first." } });
focus(box, "task:compose:text").on.keydown({ key: "Enter", ctrlKey: true });
await new Promise((r) => setTimeout(r, 0));
box = await page("t-0123456789abcdef");
focus(box, "task:compose:text").on.input({ target: { value: "Plain enter is a newline." } });
focus(box, "task:compose:text").on.keydown({ key: "Enter" });
focus(box, "task:compose:text").on.input({ target: { value: "We ship Friday." } });
const def = walk(cls(box, "d-compose")[0], (x) => x.tag === "button" && String(x.className).includes("default"))[0];
await def.on.click();
done({ Primary: cls(compose, "primary")[0].textContent, Replies: buttons.slice(1).map((b) => b.textContent), Default: def.textContent, Posts: posts });`, answers, &got)
		wantReplies, wantDefault := []string{"Send reply", "Save as draft"}, "Send reply"
		if send == "draft" {
			wantReplies, wantDefault = []string{"Save as draft", "Send reply"}, "Save as draft"
		}
		if got.Primary != "Send" || !slices.Equal(got.Replies, wantReplies) || got.Default != wantDefault {
			t.Errorf("send %s: primary %q, replies %v, default %q", send, got.Primary, got.Replies, got.Default)
		}
		want := []any{
			map[string]any{"path": "/api/tasks/t-0123456789abcdef/chat", "body": map[string]any{"text": "Take the timeout case first."}},
			map[string]any{"path": "/api/tasks/t-0123456789abcdef/respond", "body": map[string]any{"source": "cockpit", "args": map[string]any{"kind": "reply", "send": send, "text": "We ship Friday."}}},
		}
		if mustJS(t, got.Posts) != mustJS(t, want) {
			t.Errorf("send %s: posts %v, want %v", send, got.Posts, want)
		}
	}
}
