package web

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/plugins/source/fakesource"
)

// treeJS builds h(...) as a plain tree and reads it back.
const treeJS = `
function h(tag, attrs, ...c) { return { tag, attrs: attrs || {}, children: c.flat(Infinity).filter((x) => x !== null && x !== undefined && x !== false) }; }
function text(n) { if (Array.isArray(n)) return n.map(text).join(""); return n && typeof n === "object" ? (n.children || []).map(text).join("") : n === undefined || n === null ? "" : String(n); }
function all(n, pred, out = []) {
  if (Array.isArray(n)) { n.forEach((x) => all(x, pred, out)); return out; }
  if (n && typeof n === "object") { if (pred(n)) out.push(n); for (const c of n.children || []) all(c, pred, out); }
  return out;
}
const byClass = (n, c) => all(n, (x) => String(x.attrs.class || "").split(" ").includes(c));
const byFocus = (n, f) => all(n, (x) => x.attrs["data-focus"] === f)[0];
const buttonLabels = (n) => all(n, (x) => x.tag === "button").map(text);
function safeURL(u) { return u; }
function stamp(t) { return String(t); }
`

// viewsCatalog is GET /api/views with the core views the daemon serves.
func viewsCatalog() api.ViewsCatalog {
	return api.ViewsCatalog{
		Views: []api.TaskViewSpec{{ID: "focus", Label: "Focus"}, {ID: "inbox", Label: "Inbox"}, {ID: "outbox", Label: "Outbox"}, {ID: "next", Label: "Next"},
			{ID: "waiting", Label: "Waiting"}, {ID: "projects", Label: "Projects"}, {ID: "someday", Label: "Someday"}, {ID: "logbook", Label: "Logbook"}},
		Areas:    []string{"OSS", "Hiring"},
		Contexts: []string{"@decide", "@reply"},
		Verbs: []api.VerbSpec{{Verb: "approve", Label: "Approve"}, {Verb: "edit", Label: "Edit"}, {Verb: "dismiss", Label: "Discard"},
			{Verb: "retry", Label: "Retry"}},
	}
}

var coreViewIDs = []string{"focus", "inbox", "outbox", "next", "waiting", "projects", "someday", "logbook"}

// noViews stands in for views.js where a test runs app.js's loading alone.
const noViews = "const loadTasks = async () => {};\n"

// noUpdate stands in for update.js where a test runs app.js's loading alone.
const noUpdate = "const checkBuild = () => {};\n"

// viewsScripts are routes.js, views.js, review.js, task.js and detail.js, in the page's order.
func viewsScripts(t *testing.T) string {
	return staticJS(t, "routes.js") + staticJS(t, "views.js") + staticJS(t, "review.js") + staticJS(t, "task.js") + staticJS(t, "detail.js")
}

// viewsPrelude is viewsScripts and plugins.js over a tree h.
func viewsPrelude(t *testing.T) string {
	return treeJS + pluginsJS(t) + viewsScripts(t)
}

func task(id, state string, mod func(*api.TaskView)) api.TaskView {
	e := api.TaskView{ID: id, State: state, Title: "Task " + id, Units: []unit.Unit{}, Verbs: []api.VerbState{},
		Created: time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC), Updated: time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)}
	if mod != nil {
		mod(&e)
	}
	return e
}

func TestNavListsCoreThenPluginViews(t *testing.T) {
	var got struct {
		IDs    []string `json:"ids"`
		Core   []bool   `json:"core"`
		Counts []any    `json:"counts"`
	}
	counters := api.Counters{Views: map[string]int{"focus": 1, "inbox": 4, "outbox": 2, "next": 7, "waiting": 3, "projects": 0, "someday": 5, "logbook": 9}}
	nodeRun(t, catalogPrelude(t)+viewsPrelude(t), nil, `
const nav = viewNav(input.cat, catalog.views, ["fake-board"], input.counters);
process.stdout.write(JSON.stringify({ ids: nav.map((v) => v.id), core: nav.map((v) => v.core), counts: nav.filter((v) => v.core).map((v) => v.count) }));`,
		map[string]any{"cat": viewsCatalog(), "counters": counters}, &got)
	plugin := []string{"mine", "plan", "dash", "all", "explore", "recent"}
	want := slices.Concat(coreViewIDs, plugin, []string{"fake-board"})
	if !slices.Equal(got.IDs, want) {
		t.Fatalf("nav %v, want %v", got.IDs, want)
	}
	if got.IDs[0] != "focus" {
		t.Errorf("first view %q, want focus", got.IDs[0])
	}
	for i, c := range got.Core {
		if c != (i < len(coreViewIDs)) {
			t.Errorf("%s core=%v", got.IDs[i], c)
		}
	}
	for i, id := range coreViewIDs {
		if n, ok := got.Counts[i].(float64); !ok || int(n) != counters.Views[id] {
			t.Errorf("%s count %v, want %d", id, got.Counts[i], counters.Views[id])
		}
	}
}

func TestDisabledPluginViewsVanish(t *testing.T) {
	var got struct {
		IDs      []string `json:"ids"`
		View     string   `json:"view"`
		Before   string   `json:"before"`
		Enabled  string   `json:"enabled"`
		CoreStay string   `json:"core_stay"`
	}
	nodeRun(t, catalogPrelude(t)+viewsPrelude(t), nil, `
const served = catalog.views;
const nav = viewNav(input.cat, [], [], null);
process.stdout.write(JSON.stringify({
  ids: nav.map((v) => v.id),
  view: resolveView("mine", input.cat, [], () => false),
  before: resolveView("mine", null, [], () => false),
  enabled: resolveView("mine", input.cat, served, () => false),
  core_stay: resolveView("waiting", input.cat, [], () => false),
}));`, map[string]any{"cat": viewsCatalog()}, &got)
	if !slices.Equal(got.IDs, coreViewIDs) {
		t.Errorf("nav with the GitHub plugin off %v, want the core views alone", got.IDs)
	}
	if got.View != "focus" {
		t.Errorf("#view=mine with the plugin off shows %q, want focus", got.View)
	}
	if got.Before != "mine" || got.Enabled != "mine" || got.CoreStay != "waiting" {
		t.Errorf("before load %q, plugin on %q, core %q", got.Before, got.Enabled, got.CoreStay)
	}
}

func TestNavCountsComeFromThePage(t *testing.T) {
	full := api.TasksPage{Epoch: "ep", Rev: 4, Full: true, Removed: []string{},
		Tasks:    []api.TaskView{task("e-1", "inbox", nil), task("e-2", "inbox", nil)},
		Counters: api.Counters{Views: map[string]int{"inbox": 2}}}
	delta := api.TasksPage{Epoch: "ep", Rev: 6, Removed: []string{"e-1"}, Tasks: []api.TaskView{},
		Counters: api.Counters{Views: map[string]int{"inbox": 1}}}
	var got struct {
		Asked  []string `json:"asked"`
		Counts []any    `json:"counts"`
		Lens   []int    `json:"lens"`
	}
	nodeRun(t, viewsPrelude(t), nil, `
const asked = []; const pages = [input.full, input.delta];
async function getJSON(u) { asked.push(u); return u === "/api/views" ? input.cat : pages.shift(); }
const inboxCount = () => viewNav(tasksState.catalog, [], [], tasksState.counters).find((v) => v.id === "inbox").count;
(async () => {
  const route = { view: "inbox", area: "", ctx: [] };
  const counts = [], lens = [];
  await loadTasks(route);
  counts.push(inboxCount()); lens.push(tasksState.tasks.length);
  // A change carries other counters; the list reloads and its page wins.
  const change = { counters: { views: { inbox: 3 } } };
  if (change.counters) await loadTasks(route);
  counts.push(inboxCount()); lens.push(tasksState.tasks.length);
  process.stdout.write(JSON.stringify({ asked, counts, lens }));
})();`, map[string]any{"cat": viewsCatalog(), "full": full, "delta": delta}, &got)
	if len(got.Counts) != 2 || got.Counts[0] != float64(2) || got.Counts[1] != float64(1) || !slices.Equal(got.Lens, []int{2, 1}) {
		t.Errorf("counts %v with lists of %v, want the page's 2 then 1", got.Counts, got.Lens)
	}
	if want := []string{"/api/views", "/api/tasks?view=inbox", "/api/tasks?view=inbox&since=ep%3A4"}; !slices.Equal(got.Asked, want) {
		t.Errorf("asked %v, want %v", got.Asked, want)
	}
}

func TestAreaFiltersEveryView(t *testing.T) {
	var got struct {
		URLs   map[string]string `json:"urls"`
		Keys   []string          `json:"keys"`
		Route  map[string]any    `json:"route"`
		Hash   string            `json:"hash"`
		Asked  []string          `json:"asked"`
		Picker string            `json:"picker"`
	}
	nodeRun(t, viewsPrelude(t), nil, `
const urls = {};
for (const v of input.views) urls[v] = tasksURL(v, "OSS", [], "");
const asked = [];
async function getJSON(u) { asked.push(u); return u === "/api/views" ? input.cat : { epoch: "e", rev: 1, full: true, tasks: [], removed: [], counters: { views: {} } }; }
function writeHash() {}
(async () => {
  await loadTasks({ view: "inbox", area: "OSS", ctx: [] });
  await loadTasks({ view: "focus", area: "OSS", ctx: [] });
  await loadTasks({ view: "needs-me", area: "OSS", ctx: [] });
  process.stdout.write(JSON.stringify({
    urls, asked,
    keys: [viewQueryKey("inbox", "OSS", []), viewQueryKey("inbox", "", [])],
    route: readRoute(routeHash("next", "", new URLSearchParams({ area: "OSS" }))),
    hash: routeHash("next", "", new URLSearchParams({ area: "OSS" })),
    picker: text(areaPicker({ view: "inbox", area: "OSS", ctx: [], q: "" })),
  }));
})();`, map[string]any{"cat": viewsCatalog(), "views": coreViewIDs}, &got)
	for _, v := range coreViewIDs {
		if !strings.Contains(got.URLs[v], "area=OSS") {
			t.Errorf("%s ignores the area: %s", v, got.URLs[v])
		}
	}
	if got.Keys[0] == got.Keys[1] {
		t.Errorf("query keys %v: every view depends on the area", got.Keys)
	}
	if got.Route["area"] != "OSS" || got.Hash != "view=next&area=OSS" {
		t.Errorf("the area is not kept in the hash: %q -> %v", got.Hash, got.Route)
	}
	if want := []string{"/api/views", "/api/tasks?view=inbox&area=OSS", "/api/tasks?view=focus&area=OSS", "/api/counters?area=OSS"}; !slices.Equal(got.Asked, want) {
		t.Errorf("asked %v, want %v", got.Asked, want)
	}
	if !strings.HasPrefix(got.Picker, "Area") {
		t.Errorf("the area picker on every view reads %q", got.Picker)
	}
}

func TestContextChipsShowOnEveryViewOfTasks(t *testing.T) {
	var got map[string][]string
	nodeRun(t, viewsPrelude(t), nil, `
function writeHash() {}
tasksState.catalog = input.cat;
const chips = (view) => byClass(h("div", {}, contextChips({ view, area: "", ctx: [], q: "" })), "chip").map(text);
process.stdout.write(JSON.stringify({ triage: chips("triage"), gates: chips("approvals") }));`,
		map[string]any{"cat": api.ViewsCatalog{Views: []api.TaskViewSpec{{ID: "triage", Label: "Triage"}, {ID: "approvals", Label: "Approvals", Over: "gates"}},
			Contexts: []string{"@decide", "@reply"}}}, &got)
	if !slices.Equal(got["triage"], []string{"@decide", "@reply"}) || len(got["gates"]) != 0 {
		t.Errorf("chips %v, want the contexts on a view of tasks and none on a view of gates", got)
	}
}

func TestFakeProviderRowRenders(t *testing.T) {
	src := appJS(t)
	prelude := lensPrelude(t) + jsConst(t, src, "FOCUS_PAIRS") + jsFunction(t, src, "focusSlot") + jsFunction(t, src, "waitField") + jsConst(t, src, "CARD_GROUPS") + jsConst(t, src, "TAG_CHIPS") + treeJS +
		`state.busy = new Set(); state.openSlot = ""; state.specs = { version: "v", byId: {} };
const CSS = { escape: (s) => s }; const document = { querySelector: () => null };
const providerName = (id) => id;
`
	row := fakesource.Row(fakesource.Thread{Key: "T1/C1", Channel: "general thread", Title: "Can someone look at the deploy?",
		Kind: fakesource.KindWaiting, WaitsOn: "me", Reason: "a question waits on you"})
	row.Views = append(row.Views, "all")
	var got struct {
		Row, Card string
		Keys      []string
	}
	nodeRun(t, prelude, append(slices.Clone(lensFuncs), "fieldById", "foldMap", "marked", "where", "facet", "whoFacets", "countdown", "age",
		"stripTitle", "mineMark", "tagChips", "briefTags", "intakeWaiting", "intakeChip", "actionStates", "actionTitle", "actionButton", "actionButtons",
		"copySlot", "multiCopy", "openRow", "openSlot", "rowTr", "card"), `
const lens = { view: "all", q: "", ts: [], quals: [], filters: new Map(), group: "", sort: "", layout: "" };
const tr = rowTr(input, "", lens, "");
const c = card(input, "", "");
process.stdout.write(JSON.stringify({ Row: text(tr), Card: text(c), Keys: Object.keys(input.payload || {}) }));`, row, &got)
	for _, s := range []string{got.Row, got.Card} {
		if !strings.Contains(s, "Can someone look at the deploy?") || !strings.Contains(s, "#general thread") {
			t.Errorf("the fake provider's row renders %q", s)
		}
		if strings.Contains(s, "undefined") || strings.Contains(s, "#undefined") {
			t.Errorf("a pull request field leaked into %q", s)
		}
	}
}

var outboxWindow = time.Date(2026, 10, 9, 15, 10, 0, 0, time.UTC)

func outboxTasks() []api.TaskView {
	return []api.TaskView{
		task("draft:7", "inbox", func(e *api.TaskView) {
			e.Title = "Reply to the deploy thread"
			e.Gates = draftGates(api.Draft{ID: 7, Unit: "slack/T1/C1", Kind: "reply", Target: "C1", Where: `#deploys in the thread "Deploy at five"`, Text: "Deployed, all green.", State: "held", Rev: 3,
				Deadline: &outboxWindow})
			e.Verbs = []api.VerbState{{Verb: "approve", Enabled: true}, {Verb: "edit", Enabled: true}, {Verb: "dismiss", Enabled: true}, {Verb: "retry", Enabled: false}}
		}),
		task("draft:8", "inbox", func(e *api.TaskView) {
			e.Title = "Nudge the reviewer"
			e.Gates = draftGates(api.Draft{ID: 8, Unit: "github/acme/api#8", Kind: "nudge", Target: "acme/api#8", Text: "Ping?", State: "failed", Error: "rate limited", Rev: 5})
			e.Verbs = []api.VerbState{{Verb: "approve", Enabled: false}, {Verb: "edit", Enabled: true}, {Verb: "dismiss", Enabled: true}, {Verb: "retry", Enabled: true}}
		}),
	}
}

func TestOutboxListsDraftsAndApproves(t *testing.T) {
	var got struct {
		Text    string            `json:"text"`
		Buttons [][]string        `json:"buttons"`
		Posts   []map[string]any  `json:"posts"`
		Editing []string          `json:"editing"`
		Areas   map[string]string `json:"areas"`
	}
	nodeRun(t, viewsPrelude(t), nil, `
const posts = [];
detailEnv.post = async (path, body) => { posts.push({ path, body }); return { task: {} }; };
detailEnv.notice = () => {}; detailEnv.changed = () => {}; detailEnv.render = () => {};
function writeHash() {}
tasksState.catalog = input.cat;
const route = { view: "outbox", area: "", ctx: [], task: "" };
tasksState.key = viewQueryKey("outbox", "", []);
tasksState.tasks = input.tasks;
const list = () => taskList(route, "");
const first = list();
const cards = byClass(first, "d-draft");
const buttons = cards.map(buttonLabels);
(async () => {
  await byFocus(first, "outbox:approve:draft:7").attrs.onclick();
  byFocus(first, "outbox:open:edit:draft:7").attrs.onclick();
  const editing = list();
  const area = byFocus(editing, "outbox:draft-text:draft:7");
  area.attrs.oninput({ target: { value: "Deployed; two checks were retried." } });
  await byFocus(list(), "outbox:approve:draft:7").attrs.onclick();
  await byFocus(first, "outbox:dismiss:draft:8").attrs.onclick();
  await byFocus(first, "outbox:retry:draft:8").attrs.onclick();
  process.stdout.write(JSON.stringify({ text: text(first), buttons, posts, editing: buttonLabels(byClass(editing, "d-draft")[0]) }));
})();`, map[string]any{"cat": viewsCatalog(), "tasks": outboxTasks()}, &got)
	for _, s := range []string{`To #deploys in the thread "Deploy at five"`, "Deployed, all green.", "Sends at ",
		"To acme/api#8 (destination unknown)", "Ping?", "rate limited"} {
		if !strings.Contains(got.Text, s) {
			t.Errorf("the outbox lacks %q: %s", s, got.Text)
		}
	}
	if want := [][]string{{"Approve", "Edit", "Discard"}, {"Retry", "Edit", "Discard"}}; len(got.Buttons) != 2 || !slices.Equal(got.Buttons[0], want[0]) || !slices.Equal(got.Buttons[1], want[1]) {
		t.Errorf("buttons %v, want %v", got.Buttons, want)
	}
	if !slices.Equal(got.Editing, []string{"Approve", "Cancel"}) {
		t.Errorf("editing buttons %v", got.Editing)
	}
	type post struct{ path, body string }
	var posts []post
	for _, p := range got.Posts {
		posts = append(posts, post{p["path"].(string), mustJS(t, p["body"])})
	}
	want := []post{
		{"/api/tasks/draft:7/approve", `{"args":{"rev":"3"},"source":"cockpit"}`},
		{"/api/tasks/draft:7/approve", `{"args":{"rev":"3","text":"Deployed; two checks were retried."},"source":"cockpit"}`},
		{"/api/tasks/draft:8/dismiss", `{"source":"cockpit"}`},
		{"/api/tasks/draft:8/retry", `{"args":{"rev":"5"},"source":"cockpit"}`},
	}
	if !slices.Equal(posts, want) {
		t.Errorf("posts %v, want %v", posts, want)
	}
}

func TestUnitLabelFallsBackToTheUnitForEveryProvider(t *testing.T) {
	var got []string
	nodeRun(t, viewsPrelude(t), nil, `process.stdout.write(JSON.stringify([
  unitLabel({ provider: "github", key: "acme/api#7" }), unitLabel({ provider: "linear", key: "PAY-88" }), unitLabel(null)]));`, nil, &got)
	if want := []string{"github:acme/api#7", "linear:PAY-88", ""}; !slices.Equal(got, want) {
		t.Fatalf("labels %q, want %q", got, want)
	}
}

// TestAViewSaysWhichPluginsAreStillLoading checks that a list of tasks shows the page's loading plugins with
// their reasons, and a stale row its mark.
func TestAViewSaysWhichPluginsAreStillLoading(t *testing.T) {
	var got []string
	nodeRun(t, viewsPrelude(t), nil, `
function writeHash() {}
tasksState.catalog = input.cat;
applyTasksPage(viewQueryKey("inbox", "", []), input.page);
process.stdout.write(JSON.stringify(byClass(h("div", {}, ...taskList({ view: "inbox", area: "", ctx: [], q: "" }, "")), "loading").map(text)));`,
		map[string]any{"cat": api.ViewsCatalog{Views: []api.TaskViewSpec{{ID: "inbox", Label: "Inbox"}}},
			"page": api.TasksPage{Epoch: "e", Rev: 1, Full: true, Tasks: []api.TaskView{}, Loading: []api.Loading{{Plugin: "slack", Reason: "importing threads"}}}}, &got)
	if !slices.Equal(got, []string{"Loading slack: importing threads"}) {
		t.Errorf("loading notes %q", got)
	}
}

// TestATaskRowReadsItsPropertiesByRole checks that a row shows the star, the contexts, the area and the
// deadline of the properties the catalog gives those roles, whatever their names.
func TestATaskRowReadsItsPropertiesByRole(t *testing.T) {
	var got string
	nodeRun(t, viewsPrelude(t), nil, `
function writeHash() {}
tasksState.catalog = input.cat;
process.stdout.write(JSON.stringify(text(taskItem(input.task, ""))));`,
		map[string]any{"cat": api.ViewsCatalog{Props: []api.PropSpec{{Name: "pin", Type: "enum", Values: []string{"yes", "no"}, Role: "star"},
			{Name: "where", Type: "string[]", Role: "contexts"}, {Name: "team", Type: "string", Role: "area"}}},
			"task": task("e-1", "next", func(e *api.TaskView) {
				e.Props = map[string]json.RawMessage{"pin": json.RawMessage(`"yes"`), "where": json.RawMessage(`["@calls"]`), "team": json.RawMessage(`"OSS"`),
					"focus": json.RawMessage(`"on"`), "area": json.RawMessage(`"Hiring"`)}
			})}, &got)
	if !strings.Contains(got, "★") || !strings.Contains(got, "@calls") || !strings.Contains(got, "OSS") || strings.Contains(got, "Hiring") {
		t.Errorf("row %q", got)
	}
}
