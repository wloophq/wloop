package web

import (
	"strings"
	"testing"
)

func routesPrelude(t *testing.T) string {
	src := appJS(t)
	var b strings.Builder
	b.WriteString(catalogPrelude(t))
	b.WriteString(noPlugins)
	b.WriteString(`const location = { hash: "" }; const history = { replaceState: (_, __, h) => { location.hash = h; } };` + "\n")
	b.WriteString(`const window = { addEventListener() {} };` + "\n")
	b.WriteString(staticJS(t, "routes.js"))
	b.WriteString(staticJS(t, "views.js"))
	for _, c := range []string{"UNIT_KEY", "QUERY_MAX"} {
		b.WriteString(jsConst(t, src, c))
	}
	for _, f := range []string{"fieldById", "tagKeyField", "groupField", "parseSort", "readHash", "hashString"} {
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
	nodeRun(t, routesPrelude(t)+treeJS+staticJS(t, "task.js")+staticJS(t, "detail.js"), nil, `
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
process.stdout.write(JSON.stringify({ View: r.view, Task: r.task, PR: r.pr, Detail: shown && !box.hidden, List: text(taskList(r, r.task)), Back: hashString({ ...r, task: "" }), Old: old }));`, viewsCatalog(), &got)
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
