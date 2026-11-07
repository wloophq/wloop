package web

import (
	"slices"
	"strings"
	"testing"
)

// queueHarness runs queue.js over a fake daemon: queues answers GET /api/queues/<kind> and the tasks GET
// /api/tasks/<id>; posts are logged; a post to a path in refuse answers 409.
const queueHarness = `
const log = { gets: [], posts: [], bodies: [], opened: [], tasks: [], notices: [], renders: 0, nav: [], acts: [] };
const env = {
  route: () => readRoute(globalThis.hash),
  get: async (url) => {
    log.gets.push(url);
    if (url.startsWith("/api/tasks/")) {
      const id = decodeURIComponent(url.slice("/api/tasks/".length));
      const e = (input.tasks || {})[id];
      if (!e) throw new Error("no task " + id);
      return e;
    }
    return JSON.parse(JSON.stringify(input.queues[decodeURIComponent(url.split("/")[3].split("?")[0])]));
  },
  post: async (url, body) => { log.posts.push(url); log.bodies.push(body); if ((input.refuse || []).includes(url)) throw Object.assign(new Error("it changed"), { status: 409 }); return {}; },
  openSource: (e, u) => log.opened.push(u ? u.provider + ":" + u.key : ""),
  openTask: (id) => log.tasks.push(id),
  act: async (u, id) => { log.acts.push(unitPath(u, id)); if ((input.refuse || []).includes(unitPath(u, id))) throw new Error("it changed"); },
  row: (e) => h("li", { "data-task": e.id }, e.title),
  render: () => { log.renders++; },
  navigate: (v) => log.nav.push(v),
};
detailEnv.post = env.post;
detailEnv.notice = (m, err) => log.notices.push((err ? "!" : "") + m);
detailEnv.changed = () => {};
const view = () => renderQueue(queueState.queue, queueState.at, env);
const shown = () => all(view(), (x) => x.attrs["data-task"]).map((x) => x.attrs["data-task"]);
`

func queuePrelude(t *testing.T) string {
	return viewsPrelude(t) + staticJS(t, "queue.js") + queueHarness
}

func situationItem(id, title string) map[string]any {
	return map[string]any{"task": map[string]any{"id": id, "title": title, "units": []map[string]string{{"provider": "alpha", "key": "acme/api#" + id}}},
		"situation": map[string]string{"provider": "alpha", "kind": "merge", "label": "can merge"},
		"primary":   map[string]string{"kind": "open", "label": "Open at source"}}
}

func threeQueue() map[string]any {
	return map[string]any{"kind": "last-step", "title": "Last steps", "empty": "Nothing waits.", "total": 3,
		"items": []any{situationItem("t-1", "one"), situationItem("t-2", "two"), situationItem("t-3", "three")}}
}

func TestQueueShowsOneItemWithProgress(t *testing.T) {
	var got struct {
		Progress, NextUp string
		Shown            []string
		Primary          string
	}
	nodeRun(t, queuePrelude(t), nil, `
(async () => {
globalThis.hash = "#queue=last-step";
await queueLoad(env.route(), env);
const nodes = view();
process.stdout.write(JSON.stringify({ Progress: text(byClass(nodes, "progress")), NextUp: text(byClass(nodes, "next-up")), Shown: shown(), Primary: text(byClass(nodes, "queue-primary")) }));
})();`, map[string]any{"queues": map[string]any{"last-step": threeQueue()}}, &got)
	if got.Progress != "1 of 3" || !slices.Equal(got.Shown, []string{"t-1", "t-2"}) || !strings.Contains(got.NextUp, "+1 more") || !strings.Contains(got.Primary, "Open at source") {
		t.Fatalf("%+v", got)
	}
}

func TestQueueEnterRunsPrimaryGate(t *testing.T) {
	gateItem := map[string]any{"task": map[string]any{"id": "t-1", "title": "ship", "units": []any{}},
		"gate":    map[string]any{"id": 7, "kind": "question", "text": "Ship it?", "intents": []map[string]any{{"id": "approve", "label": "Approve"}, {"id": "dismiss", "label": "Dismiss"}}},
		"primary": map[string]string{"kind": "gate", "id": "approve", "label": "Approve"}}
	var got struct {
		Posts []string
		Gets  int
	}
	nodeRun(t, queuePrelude(t), nil, `
(async () => {
globalThis.hash = "#queue=answer";
await queueLoad(env.route(), env);
queueKeyAction("queue:primary", env);
queueKeyAction("queue:primary", env);
await new Promise((r) => setTimeout(r, 10));
process.stdout.write(JSON.stringify({ Posts: log.posts, Gets: log.gets.length }));
})();`, map[string]any{"queues": map[string]any{"answer": map[string]any{"kind": "answer", "title": "Agent questions", "total": 1, "items": []any{gateItem}}}}, &got)
	if !slices.Equal(got.Posts, []string{"/api/gates/7/approve"}) || got.Gets != 2 {
		t.Fatalf("posts %v, gets %d: Enter posts the gate's primary once, then the queue reloads", got.Posts, got.Gets)
	}
}

func TestQueueEnterOpenAtSource(t *testing.T) {
	var got struct {
		Opened, Posts []string
	}
	nodeRun(t, queuePrelude(t), nil, `
(async () => {
globalThis.hash = "#queue=last-step";
await queueLoad(env.route(), env);
await queueAct(queueState.queue.items[0], env);
process.stdout.write(JSON.stringify({ Opened: log.opened, Posts: log.posts }));
})();`, map[string]any{"queues": map[string]any{"last-step": threeQueue()}}, &got)
	if !slices.Equal(got.Opened, []string{"alpha:acme/api#t-1"}) || len(got.Posts) != 0 {
		t.Fatalf("%+v", got)
	}
}

func TestQueueSkipAndBack(t *testing.T) {
	var got struct{ AfterSkip, AfterBack, AtEnd string }
	nodeRun(t, queuePrelude(t), nil, `
(async () => {
globalThis.hash = "#queue=last-step";
await queueLoad(env.route(), env);
queueKeyAction("queue:skip", env);
const afterSkip = text(byClass(view(), "progress"));
queueKeyAction("queue:back", env);
const afterBack = text(byClass(view(), "progress"));
for (let i = 0; i < 5; i++) queueKeyAction("queue:skip", env);
process.stdout.write(JSON.stringify({ AfterSkip: afterSkip, AfterBack: afterBack, AtEnd: text(byClass(view(), "progress")) }));
})();`, map[string]any{"queues": map[string]any{"last-step": threeQueue()}}, &got)
	if got.AfterSkip != "2 of 3" || got.AfterBack != "1 of 3" || got.AtEnd != "3 of 3" {
		t.Fatalf("%+v", got)
	}
}

func TestQueueStaleItemConflict(t *testing.T) {
	item := map[string]any{"task": map[string]any{"id": "t-1", "title": "ship", "units": []map[string]string{{"provider": "alpha", "key": "acme/api#1"}}},
		"situation": map[string]string{"provider": "alpha", "kind": "no_reviewer", "label": "no reviewer"},
		"primary":   map[string]string{"kind": "action", "id": "request-review", "label": "Request a review"}}
	var got struct {
		Posts, Acts []string
		Gets        int
		Error       string
	}
	nodeRun(t, queuePrelude(t), nil, `
(async () => {
globalThis.hash = "#queue=pick-someone";
await queueLoad(env.route(), env);
await queueAct(queueState.queue.items[0], env);
process.stdout.write(JSON.stringify({ Posts: log.posts, Acts: log.acts, Gets: log.gets.length, Error: text(byClass(view(), "err")) }));
})();`, map[string]any{"queues": map[string]any{"pick-someone": map[string]any{"kind": "pick-someone", "title": "Pick someone", "total": 1, "items": []any{item}}},
		"refuse": []string{"/api/units/alpha/acme%2Fapi%231/request-review"}}, &got)
	if len(got.Acts) != 1 || len(got.Posts) != 0 || got.Gets != 2 || !strings.Contains(got.Error, "it changed") {
		t.Fatalf("%+v: a refused action shows why, the queue reloads, nothing runs twice", got)
	}
}

func TestQueueActionRunsThroughTheActionPath(t *testing.T) {
	item := map[string]any{"task": map[string]any{"id": "t-1", "title": "ship", "units": []map[string]string{{"provider": "alpha", "key": "acme/api#1"}}},
		"situation": map[string]string{"provider": "alpha", "kind": "stalled", "label": "stalled"},
		"primary":   map[string]string{"kind": "action", "id": "take_over", "label": "Take over"}}
	var got struct{ Posts, Acts []string }
	nodeRun(t, queuePrelude(t), nil, `
(async () => {
globalThis.hash = "#queue=last-step";
await queueLoad(env.route(), env);
queueKeyAction("queue:primary", env);
await new Promise((r) => setTimeout(r, 5));
process.stdout.write(JSON.stringify({ Posts: log.posts, Acts: log.acts }));
})();`, map[string]any{"queues": map[string]any{"last-step": map[string]any{"kind": "last-step", "title": "Last steps", "total": 1, "items": []any{item}}}}, &got)
	if !slices.Equal(got.Acts, []string{"/api/units/alpha/acme%2Fapi%231/take_over"}) || len(got.Posts) != 0 {
		t.Fatalf("%+v: an action primary runs through the cockpit's action path (its confirm, input and basis), never a raw post", got)
	}
}

func TestQueueEnterApprovesTheDraftRevisionShown(t *testing.T) {
	draft := map[string]any{"id": 4, "gate": 7, "rev": 3, "text": "Thanks, merged.", "delivery": "reply", "unit": "alpha:acme/api#1"}
	item := map[string]any{"task": map[string]any{"id": "t-1", "title": "ship", "units": []any{}},
		"gate":    map[string]any{"id": 7, "kind": "draft", "delivery": "held", "subject": draft, "intents": []map[string]any{{"id": "approve", "label": "Send"}}},
		"primary": map[string]string{"kind": "gate", "id": "approve", "label": "Send"}}
	var got struct {
		Posts  []string
		Bodies []map[string]any
	}
	nodeRun(t, queuePrelude(t), nil, `
(async () => {
globalThis.hash = "#queue=send-draft";
await queueLoad(env.route(), env);
queueKeyAction("queue:primary", env);
await new Promise((r) => setTimeout(r, 5));
process.stdout.write(JSON.stringify({ Posts: log.posts, Bodies: log.bodies }));
})();`, map[string]any{"queues": map[string]any{"send-draft": map[string]any{"kind": "send-draft", "title": "Send drafts", "total": 1, "items": []any{item}}}}, &got)
	if !slices.Equal(got.Posts, []string{"/api/gates/7/approve"}) || mustJSON(t, got.Bodies[0]["args"]) != `{"rev":"3"}` {
		t.Fatalf("%+v: Enter on a draft approves the revision on screen", got)
	}
}

func TestQueueEmptyState(t *testing.T) {
	var got struct {
		Text  string
		Hrefs []string
	}
	nodeRun(t, queuePrelude(t), nil, `
const nodes = renderQueue({ kind: "answer", title: "Agent questions", empty: "No question waits for you.", total: 0, items: [] }, 0, env);
process.stdout.write(JSON.stringify({ Text: text(nodes), Hrefs: all(nodes, (x) => x.tag === "a").map((a) => a.attrs.href) }));`, nil, &got)
	if !strings.Contains(got.Text, "No question waits for you.") || !slices.Contains(got.Hrefs, "#view=home") {
		t.Fatalf("%+v", got)
	}
}

func TestQueueListRoute(t *testing.T) {
	var got struct {
		Gets   []string
		Shown  []string
		Opened []string
	}
	nodeRun(t, queuePrelude(t), nil, `
(async () => {
globalThis.hash = "#queue=list&ids=t-2,t-gone,t-1";
await queueLoad(env.route(), env);
const s = shown();
queueKeyAction("queue:primary", env);
await new Promise((r) => setTimeout(r, 5));
process.stdout.write(JSON.stringify({ Gets: log.gets, Shown: s, Opened: log.tasks }));
})();`, map[string]any{"tasks": map[string]any{"t-1": map[string]any{"id": "t-1", "title": "one"}, "t-2": map[string]any{"id": "t-2", "title": "two"}}}, &got)
	if !slices.Equal(got.Gets, []string{"/api/tasks/t-2", "/api/tasks/t-gone", "/api/tasks/t-1"}) || !slices.Equal(got.Shown, []string{"t-2", "t-1"}) || !slices.Equal(got.Opened, []string{"t-2"}) {
		t.Fatalf("%+v", got)
	}
}

func TestQueueNeverShowsAnotherRoutesQueue(t *testing.T) {
	gateItem := map[string]any{"task": map[string]any{"id": "t-9", "title": "ship", "units": []any{}},
		"gate":    map[string]any{"id": 7, "kind": "question", "text": "Ship it?", "intents": []map[string]any{{"id": "approve", "label": "Approve"}}},
		"primary": map[string]string{"kind": "gate", "id": "approve", "label": "Approve"}}
	var got struct {
		Switched, Failed, Recovered []string
		SwitchedText, FailedErr     string
		RecoveredErr                string
		Ran                         bool
		Posts                       []string
	}
	nodeRun(t, queuePrelude(t), nil, `
(async () => {
const shownNow = () => all(renderQueue(queueShown(env.route()), queueState.at, env), (x) => x.attrs["data-task"]).map((x) => x.attrs["data-task"]);
globalThis.hash = "#queue=answer";
await queueLoad(env.route(), env);
globalThis.hash = "#queue=send-draft";
const switched = shownNow();
const switchedText = text(renderQueue(queueShown(env.route()), queueState.at, env));
const ran = queueKeyAction("queue:primary", env);
await queueLoad(env.route(), env);
const failed = shownNow();
const failedErr = text(byClass(renderQueue(queueShown(env.route()), queueState.at, env), "err"));
queueKeyAction("queue:primary", env);
await new Promise((r) => setTimeout(r, 5));
globalThis.hash = "#queue=last-step";
await queueLoad(env.route(), env);
const recovered = shownNow();
process.stdout.write(JSON.stringify({ Switched: switched, SwitchedText: switchedText, Ran: ran, Failed: failed, FailedErr: failedErr, Recovered: recovered,
  RecoveredErr: text(byClass(view(), "err")), Posts: log.posts }));
})();`, map[string]any{"queues": map[string]any{"answer": map[string]any{"kind": "answer", "title": "Agent questions", "total": 1, "items": []any{gateItem}}, "last-step": threeQueue()}}, &got)
	if len(got.Switched) != 0 || strings.Contains(got.SwitchedText, "Agent questions") || got.Ran || len(got.Failed) != 0 || len(got.Posts) != 0 {
		t.Errorf("another route's queue shows or acts: %+v", got)
	}
	if !strings.Contains(got.FailedErr, "Could not load the queue") {
		t.Errorf("a failed load shows no error: %+v", got)
	}
	if !slices.Equal(got.Recovered, []string{"t-1", "t-2"}) || got.RecoveredErr != "" {
		t.Errorf("a successful load keeps the last error: %+v", got)
	}
}

func TestDigitsAnswerAGateCardOutsideTheDetail(t *testing.T) {
	var got struct {
		Queue, List, Detail, None string
	}
	nodeRun(t, queuePrelude(t)+jsFunction(t, appJS(t), "pageGates"), nil, `
const gate = (id) => ({ id, kind: "question", intents: [{ id: "answer" }, { id: "continue" }] });
const inCard = (id) => ({ closest: (s) => (s === ".d-gate" ? { getAttribute: () => String(id) } : null) });
globalThis.hash = "#queue=answer";
globalThis.location = { hash: "#queue=answer" };
queueState.key = queueKey(readRoute("#queue=answer"));
queueState.queue = { items: [{ task: { id: "t-1" }, gate: gate(7) }] };
tasksState.gates = [Object.assign(gate(8), { task: "t-2" })];
tasksState.tasks = [{ id: "t-2" }];
detailState.task = { id: "t-3", gates: [gate(9)] };
const at = (id) => { const q = gateAt(inCard(id), pageGates()); return q ? q.task.id + ":" + q.gate.id : ""; };
process.stdout.write(JSON.stringify({ Queue: at(7), List: at(8), Detail: at(9), None: at(10) }));`, nil, &got)
	if got.Queue != "t-1:7" || got.List != "t-2:8" || got.Detail != "t-3:9" || got.None != "" {
		t.Fatalf("%+v: a digit answers the focused gate card wherever the page draws it", got)
	}
}
