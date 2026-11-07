package web

import (
	"strings"
	"testing"
)

func routesPrelude(t *testing.T) string {
	src := appJS(t)
	var b strings.Builder
	b.WriteString(noPlugins)
	b.WriteString(`const location = { hash: "" }; const history = { replaceState: (_, __, h) => { location.hash = h; } };` + "\n")
	b.WriteString(`const window = { addEventListener() {} };` + "\n")
	b.WriteString(staticJS(t, "row.js"))
	b.WriteString(staticJS(t, "routes.js"))
	b.WriteString(staticJS(t, "outbox.js"))
	b.WriteString(staticJS(t, "views.js"))
	b.WriteString(staticJS(t, "toolbar.js"))
	b.WriteString(taskLensJS(t))
	for _, c := range []string{"UNIT_KEY", "QUERY_MAX"} {
		b.WriteString(jsConst(t, src, c))
	}
	for _, f := range []string{"fold", "readHash", "hashString"} {
		b.WriteString(jsFunction(t, src, f))
	}
	return b.String()
}

func TestTaskHashOpensTheTask(t *testing.T) {
	var got struct {
		View, Task, PR string
		Detail         bool
		List           string
		Back           string
		Old            string
	}
	nodeRun(t, routesPrelude(t)+treeJS+staticJS(t, "window.js")+staticJS(t, "task.js")+staticJS(t, "detail.js"), nil, `
tasksState.catalog = input;
tasksState.key = viewQueryKey("fits-now", "", []);
tasksState.tasks = [{ id: "e-1", state: "next", title: "Write the release notes", contexts: [], units: [] }];
location.hash = "#view=fits-now&entry=e-1";
const old = readHash().task;
location.hash = "#view=fits-now&task=e-1";
const r = readHash();
const box = { dataset: {}, hidden: true, replaceChildren() {}, contains: () => false };
globalThis.document = { activeElement: null, body: { classList: { add() {} } } };
const shown = renderTaskDetail(box, r);
process.stdout.write(JSON.stringify({ View: r.view, Task: r.task, PR: r.pr, Detail: shown && !box.hidden, List: text(taskListNodes(r, r.task)), Back: hashString({ ...r, task: "" }), Old: old }));`, viewsCatalog(), &got)
	if got.View != "fits-now" || got.Task != "e-1" || got.PR != "" {
		t.Errorf("#view=fits-now&task=e-1 reads view %q task %q unit %q", got.View, got.Task, got.PR)
	}
	if !got.Detail || !strings.Contains(got.List, "Write the release notes") {
		t.Errorf("detail open %v, list %q", got.Detail, got.List)
	}
	if got.Back != "view=fits-now" {
		t.Errorf("closing the task leaves %q, want view=fits-now", got.Back)
	}
	if got.Old != "" {
		t.Errorf("the old entry= hash still opens task %q", got.Old)
	}
}

func TestFirstLoadResolvesACoreViewOnceItsCatalogIsIn(t *testing.T) {
	var got struct{ Asked []string }
	nodeRun(t, routesPrelude(t), nil, `
const asked = [];
async function getJSON(u) { asked.push(u); return u === "/api/views" ? input : { epoch: "e", rev: 1, full: true, tasks: [], removed: [], counters: { views: {} } }; }
location.hash = "#view=focus";
(async () => {
  await loadTasks();
  process.stdout.write(JSON.stringify({ Asked: asked }));
})();`, viewsCatalog(), &got)
	if want := "/api/views,/api/tasks?view=focus"; strings.Join(got.Asked, ",") != want {
		t.Errorf("first load asked %v, want %s", got.Asked, want)
	}
}

func TestReadRouteReadsThePanel(t *testing.T) {
	var got struct{ Panel, Q, None string }
	nodeRun(t, routesPrelude(t), nil, `
const p = readRoute("#panel=omnibox&q=abc");
process.stdout.write(JSON.stringify({ Panel: p.panel, Q: p.q, None: readRoute("#view=inbox").panel }));`, nil, &got)
	if got.Panel != "omnibox" || got.Q != "abc" || got.None != "" {
		t.Fatalf("got %+v", got)
	}
}

func TestHashWritesKeepTheQueue(t *testing.T) {
	var got []struct{ From, To, Before, After string }
	nodeRun(t, routesPrelude(t)+staticJS(t, "queue.js"), nil, `
tasksState.catalog = input;
const out = [];
for (const [from, change] of [["#queue=list&ids=t-1,t-2", { task: "t-1", pr: "" }], ["#queue=answer", { task: "t-3", pr: "" }],
  ["#queue=send-draft&task=t-3", { task: "" }], ["#queue=list&ids=t-1,t-2&task=t-1", { q: "x" }]]) {
  location.hash = from;
  const before = queueKey(readRoute(location.hash));
  location.hash = "#" + hashString({ ...readHash(), ...change });
  out.push({ From: from, To: location.hash, Before: before, After: queueKey(readRoute(location.hash)) });
}
process.stdout.write(JSON.stringify(out));`, viewsCatalog(), &got)
	if len(got) != 4 {
		t.Fatalf("%+v", got)
	}
	for _, g := range got {
		if g.Before != g.After {
			t.Errorf("%s became %s: queue %q, then %q", g.From, g.To, g.Before, g.After)
		}
	}
}
