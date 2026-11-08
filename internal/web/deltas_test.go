package web

import (
	"slices"
	"strings"
	"testing"
)

const deltaPrelude = `const state = { specs: { version: "", byId: {} }, plan: { items: [], closed: [] }, rows: [], epoch: "", rev: 0, detail: null, health: null, error: "", busy: new Set(), answered: new Map(), conn: "" };
const API_VERSION = "1";
const requests = [], timers = [], sources = [];
let pending, queued = null, chain = Promise.resolve(), retries = 0, opened = false, hash = { view: "needs-me", pr: "" };
const setTimeout = (f) => timers.push(f);
const clearTimeout = () => {};
const readHash = () => hash;
const render = () => {}, refreshSpecs = async () => {}, loadExploreRecent = async () => {};
const loadPlan = async () => { requests.push("plan"); }, loadRoundGates = async () => { requests.push("gates"); };
const catalog = { version: "" };
const applyCatalog = (c) => { catalog.version = c.version || ""; };
const loadActivity = async () => { requests.push("activity"); };
const loadDetail = async () => { requests.push("detail"); };
class EventSource { constructor() { sources.push(this); } }
EventSource.CLOSED = 2;
const unit = (k) => ({ provider: "github", key: k });
const row = (k, title) => ({ unit: unit(k), label: k, title });
let pages = {};
` + noPlugins + noViews + noUpdate + noScreens + `const getJSON = async (url) => {
  requests.push(url);
  if (url === "/api/health") return { viewer: "me" };
  if (url === "/api/catalog") return { version: "c1", views: [], facets: [], situations: [] };
  if (pages[url]) return pages[url];
  throw new Error("unexpected " + url);
};
const flush = async () => { while (timers.length) timers.shift()(); await chain; };
const message = (c) => sources[0].onmessage({ data: JSON.stringify({ type: "changed", epoch: "e1", ...c }) });
const full = { epoch: "e1", rev: 1, full: true, rows: [row("a/b#1", "one"), row("a/b#2", "two")], removed: [] };
` + deltaSince

const deltaSince = `const since = (epoch, rev) => "/api/rows?since=" + encodeURIComponent(epoch + ":" + rev);
`

var deltaFuncs = []string{"load", "mergeRows", "applyChange", "scheduleLoad", "onChange", "connect"}

func runDelta(t *testing.T, main string) (out struct {
	Requests []string `json:"requests"`
	Titles   []string `json:"titles"`
	Rev      uint64   `json:"rev"`
	Error    string   `json:"error"`
}) {
	t.Helper()
	nodeRun(t, deltaPrelude+keyJS(t), deltaFuncs, `(async () => {
  pages["/api/rows"] = full;
  await load();
  connect();
  sources[0].onopen();
  requests.length = 0;
`+main+`
  process.stdout.write(JSON.stringify({ requests, titles: state.rows.map((r) => r.title), rev: state.rev, error: state.error }));
})();`, nil, &out)
	return out
}

func TestCockpitBootLoadsOnce(t *testing.T) {
	var got struct {
		Rows []string `json:"rows"`
	}
	nodeRun(t, deltaPrelude+keyJS(t), deltaFuncs, `(async () => {
  pages["/api/rows"] = full;
  await load();
  connect();
  sources[0].onopen();
  await flush();
  process.stdout.write(JSON.stringify({ rows: requests.filter((u) => u.startsWith("/api/rows")) }));
})();`, nil, &got)
	if !slices.Equal(got.Rows, []string{"/api/rows"}) {
		t.Fatalf("rows requests %v", got.Rows)
	}
}

func TestCockpitMergesADelta(t *testing.T) {
	got := runDelta(t, `
  pages[since("e1", 1)] = { epoch: "e1", rev: 3, full: false, rows: [row("a/b#2", "two again"), row("a/b#3", "three")], removed: [unit("a/b#1")] };
  message({ rev: 3, units: [unit("a/b#1"), unit("a/b#2"), unit("a/b#3")] });
  await flush();`)
	if want := []string{"/api/rows?since=e1%3A1"}; !slices.Equal(got.Requests, want) {
		t.Errorf("requests %v, want %v", got.Requests, want)
	}
	if want := []string{"two again", "three"}; !slices.Equal(got.Titles, want) || got.Rev != 3 {
		t.Errorf("rows %v at rev %d, want %v at 3", got.Titles, got.Rev, want)
	}
}

func TestCockpitDetailRefetchOnlyForItsRef(t *testing.T) {
	got := runDelta(t, `
  hash = { view: "needs-me", pr: "github:a/b#1" };
  pages[since("e1", 1)] = { epoch: "e1", rev: 2, full: false, rows: [row("a/b#2", "two")], removed: [] };
  message({ rev: 2, units: [unit("a/b#2")] });
  await flush();
  pages[since("e1", 2)] = { epoch: "e1", rev: 3, full: false, rows: [row("a/b#1", "one")], removed: [] };
  message({ rev: 3, units: [unit("a/b#1")] });
  await flush();
  message({ rev: 3, health: true });
  await flush();`)
	want := []string{"/api/rows?since=e1%3A1", "/api/rows?since=e1%3A2", "detail", "/api/health"}
	if !slices.Equal(got.Requests, want) {
		t.Errorf("requests %v, want %v", got.Requests, want)
	}
}

func TestCockpitAllMessageReloadsFull(t *testing.T) {
	got := runDelta(t, `
  pages["/api/rows"] = { epoch: "e2", rev: 1, full: true, rows: [row("a/b#9", "nine")], removed: [] };
  message({ epoch: "e2", rev: 1, all: true });
  await flush();`)
	if !slices.Contains(got.Requests, "/api/rows") || len(got.Titles) != 1 || got.Titles[0] != "nine" {
		t.Errorf("requests %v, rows %v", got.Requests, got.Titles)
	}
	for _, u := range got.Requests {
		if strings.Contains(u, "since") {
			t.Errorf("a full reload pulled %s", u)
		}
	}
}

func TestCockpitBuildsNoGitHubURL(t *testing.T) {
	src := appJS(t)
	for _, fn := range []string{"candidateURL", "emptyNote"} {
		if body := jsFunction(t, src, fn); strings.Contains(body, "github.com") {
			t.Errorf("%s builds a GitHub URL", fn)
		}
	}
}

func TestCockpitFallsBackToAFullLoad(t *testing.T) {
	got := runDelta(t, `
  pages["/api/rows"] = { epoch: "e2", rev: 1, full: true, rows: [row("a/b#9", "nine")], removed: [] };
  pages[since("e1", 1)] = { epoch: "e2", rev: 2, full: false, rows: [row("a/b#1", "stale")], removed: [] };
  message({ rev: 2, units: [unit("a/b#1")] });
  await flush();
  delete pages[since("e2", 1)];
  message({ epoch: "e2", rev: 3, units: [unit("a/b#9")] });
  await flush();`)
	want := []string{"/api/rows?since=e1%3A1", "/api/plugins", "/api/catalog", "/api/rows", "/api/health", "/api/rows?since=e2%3A1", "/api/plugins", "/api/catalog", "/api/rows", "/api/health"}
	got.Requests = slices.DeleteFunc(got.Requests, func(u string) bool { return !strings.HasPrefix(u, "/api/") })
	if !slices.Equal(got.Requests, want) || !slices.Equal(got.Titles, []string{"nine"}) {
		t.Errorf("requests %v, rows %v", got.Requests, got.Titles)
	}
}

func TestCockpitReopenPullsFromTheRev(t *testing.T) {
	got := runDelta(t, `
  pages[since("e1", 1)] = { epoch: "e1", rev: 1, full: false, rows: [], removed: [] };
  sources[0].onopen();
  await flush();`)
	want := []string{"/api/rows?since=e1%3A1", "/api/health", "plan", "gates"}
	if !slices.Equal(got.Requests, want) {
		t.Errorf("requests %v, want %v: a reconnect reloads everything the cockpit holds", got.Requests, want)
	}
}

// fetched are the requests of a delta run that read the plan, the round gates or the health.
func fetched(reqs []string) []string {
	return slices.DeleteFunc(slices.Clone(reqs), func(u string) bool { return u != "plan" && u != "gates" && u != "/api/health" })
}

func TestCockpitFetchesOnlyWhatAChangeNames(t *testing.T) {
	for _, c := range []struct {
		name, message string
		want          []string
	}{
		{"health", `message({ rev: 1, health: true });`, []string{"/api/health"}},
		{"rows", `pages[since("e1", 1)] = { epoch: "e1", rev: 2, full: false, rows: [row("a/b#2", "two")], removed: [] };
  message({ rev: 2, units: [unit("a/b#2")] });`, nil},
		{"plan", `message({ rev: 1, plan: true });`, []string{"plan"}},
		{"gates", `message({ rev: 1, gates: true });`, []string{"gates"}},
		{"notify", `sources[0].onmessage({ data: JSON.stringify({ type: "notify", epoch: "e1", rev: 1, notices: [{ id: "n1", text: "hi" }] }) });`, nil},
		{"a row the plan holds", `state.plan.items = [{ id: "i1", unit: unit("a/b#2") }];
  pages[since("e1", 1)] = { epoch: "e1", rev: 2, full: false, rows: [row("a/b#2", "two")], removed: [] };
  message({ rev: 2, units: [unit("a/b#2")] });`, []string{"plan"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := runDelta(t, "\n  "+c.message+"\n  await flush();")
			if f := fetched(got.Requests); !slices.Equal(f, c.want) {
				t.Errorf("fetched %v, want %v (requests %v)", f, c.want, got.Requests)
			}
		})
	}
}

func TestCockpitCoalescesMessagesIntoOnePull(t *testing.T) {
	got := runDelta(t, `
  pages[since("e1", 1)] = { epoch: "e1", rev: 3, full: false, rows: [row("a/b#1", "uno"), row("a/b#2", "dos")], removed: [] };
  message({ rev: 2, units: [unit("a/b#1")] });
  message({ rev: 3, units: [unit("a/b#2")] });
  await flush();
  pages[since("e1", 3)] = { epoch: "e1", rev: 2, full: false, rows: [row("a/b#1", "older")], removed: [] };
  message({ rev: 2, units: [unit("a/b#1")] });
  await flush();`)
	pulls := 0
	for _, u := range got.Requests {
		if strings.HasPrefix(u, "/api/rows") {
			pulls++
		}
	}
	if pulls != 2 || !slices.Equal(got.Titles, []string{"uno", "dos"}) || got.Rev != 3 {
		t.Errorf("requests %v, rows %v at rev %d", got.Requests, got.Titles, got.Rev)
	}
}

func TestCockpitClearsTheBannerWhenADeltaSucceeds(t *testing.T) {
	got := runDelta(t, `
  state.error = "Could not reach the daemon: boom";
  pages[since("e1", 1)] = { epoch: "e1", rev: 1, full: false, rows: [], removed: [] };
  sources[0].onopen();
  await flush();`)
	if got.Error != "" {
		t.Errorf("error %q", got.Error)
	}
}

func TestLoadKeepsNewerRowsFromAnOlderPage(t *testing.T) {
	got := runDelta(t, `
  state.rev = 5;
  pages["/api/rows"] = { epoch: "e1", rev: 3, full: true, rows: [row("a/b#9", "older")], removed: [] };
  await load();
  const kept = state.rows.map((r) => r.title).join(",") + "@" + state.rev;
  pages["/api/rows"] = { epoch: "e2", rev: 1, full: true, rows: [row("a/b#9", "other epoch")], removed: [] };
  await load();
  requests.push(kept, state.rows.map((r) => r.title).join(",") + "@" + state.rev + "/" + state.epoch);`)
	n := len(got.Requests)
	if got.Requests[n-2] != "one,two@5" || got.Requests[n-1] != "other epoch@1/e2" {
		t.Errorf("requests %v", got.Requests)
	}
}

func TestDefaultOrderBreaksTiesByProviderThenKey(t *testing.T) {
	var got []string
	nodeRun(t, keyJS(t), []string{"sm", "sinceTime", "cmp", "byDefault"}, `const rows = input.map(([p, k]) => ({ unit: { provider: p, key: k }, label: k, summary: { stalled: false, since: "2026-10-01T00:00:00Z" } }));
process.stdout.write(JSON.stringify(rows.sort(byDefault).map((r) => r.unit.provider + ":" + r.unit.key)));`,
		[][2]string{{"github", "b/c#2"}, {"gitlab", "a/a#1"}, {"github", "a/b#10"}}, &got)
	if want := []string{"github:a/b#10", "github:b/c#2", "gitlab:a/a#1"}; !slices.Equal(got, want) {
		t.Errorf("order %v, want %v", got, want)
	}
}

func TestCockpitRefetchesTheDetailWhateverTheCase(t *testing.T) {
	got := runDelta(t, `
  hash = { view: "needs-me", pr: "github:A/B#1" };
  pages[since("e1", 1)] = { epoch: "e1", rev: 2, full: false, rows: [row("a/b#1", "one")], removed: [] };
  message({ rev: 2, units: [unit("a/b#1")] });
  await flush();`)
	if !slices.Contains(got.Requests, "detail") {
		t.Errorf("requests %v", got.Requests)
	}
}

func TestGetJSONAbortsAHungRequest(t *testing.T) {
	var got struct {
		Timeout bool `json:"timeout"`
	}
	nodeRun(t, `const API_VERSION = "1"; const state = {}; const apiError = () => "x"; const REQUEST_TIMEOUT_MS = 20;
const fetch = (url, opts) => new Promise((_, reject) => opts.signal.addEventListener("abort", () => reject(new Error("aborted"))));
`, []string{"getJSON"}, `setTimeout(() => {}, 500); getJSON("/x").then(() => process.stdout.write('{"timeout":false}'), () => process.stdout.write('{"timeout":true}'));`, nil, &got)
	if !got.Timeout {
		t.Error("a hung request was not aborted")
	}
}

// runTasksDelta is runDelta with a loadTasks that records the units each call names, and a forgetTasksPage
// that records its call.
func runTasksDelta(t *testing.T, main string) []string {
	t.Helper()
	prelude := strings.Replace(deltaPrelude, noViews, `const loadTasks = async (route, units) => { requests.push("tasks " + (units || []).join(",")); };
const forgetTasksPage = () => { requests.push("forget"); };
`, 1)
	if prelude == deltaPrelude {
		t.Fatal("the delta prelude has no loadTasks to replace")
	}
	var out struct {
		Requests []string `json:"requests"`
	}
	nodeRun(t, prelude+keyJS(t), deltaFuncs, `(async () => {
  pages["/api/rows"] = full;
  await load();
  connect();
  sources[0].onopen();
  requests.length = 0;
`+main+`
  process.stdout.write(JSON.stringify({ requests }));
})();`, nil, &out)
	return out.Requests
}

func TestAChangeWithCountersRefetchesTheTasksOfItsUnits(t *testing.T) {
	got := runTasksDelta(t, `
  pages[since("e1", 1)] = { epoch: "e1", rev: 2, full: false, rows: [row("a/b#1", "one")], removed: [] };
  message({ rev: 2, units: [unit("a/b#1")], counters: { views: {} } });
  await flush();`)
	if !slices.Contains(got, "tasks github:a/b#1") {
		t.Errorf("requests %v, want the tasks of github:a/b#1", got)
	}
}

func TestAnAllChangeReadsTheTasksWhole(t *testing.T) {
	got := runTasksDelta(t, `
  message({ rev: 2, all: true, counters: { views: {} } });
  await flush();`)
	if i := slices.Index(got, "forget"); i < 0 || i > slices.Index(got, "tasks ") {
		t.Errorf("requests %v, want the tasks page forgotten before the tasks load", got)
	}
}

func TestARowsPageOfAnotherCatalogRereadsTheCatalog(t *testing.T) {
	got := runDelta(t, `
  pages[since("e1", 1)] = { epoch: "e1", rev: 2, full: false, rows: [], removed: [], catalog: "c1" };
  message({ rev: 2, units: [unit("a/b#1")] });
  await flush();
  pages[since("e1", 2)] = { epoch: "e1", rev: 3, full: false, rows: [], removed: [], catalog: "c2" };
  message({ rev: 3, units: [unit("a/b#1")] });
  await flush();`)
	want := []string{"/api/rows?since=e1%3A1", "/api/rows?since=e1%3A2", "/api/catalog"}
	if !slices.Equal(got.Requests, want) {
		t.Errorf("requests %v, want %v", got.Requests, want)
	}
}

func TestAViewsVersionChangeRereadsTheViewsCatalog(t *testing.T) {
	var got struct {
		Asked []string `json:"asked"`
	}
	nodeRun(t, viewsPrelude(t), nil, `
const asked = [];
const page = (rev, specs) => ({ epoch: "ep", rev, full: rev === 1, tasks: [], removed: [], counters: { views: {} }, specs });
const pages = [page(1, "v1"), page(2, "v2"), page(3, "v2")];
const cats = [{ ...input.cat, version: "v1" }, { ...input.cat, version: "v2" }];
async function getJSON(u) { asked.push(u); return u === "/api/views" ? cats.shift() : pages.shift(); }
(async () => {
  const route = { view: "inbox", area: "", ctx: [] };
  await loadTasks(route);
  await loadTasks(route);
  await loadTasks(route);
  asked.push(tasksState.catalog.version);
  process.stdout.write(JSON.stringify({ asked }));
})();`, map[string]any{"cat": viewsCatalog()}, &got)
	want := []string{"/api/views", "/api/tasks?view=inbox", "/api/tasks?view=inbox&since=ep%3A1", "/api/views", "/api/tasks?view=inbox&since=ep%3A2", "v2"}
	if !slices.Equal(got.Asked, want) {
		t.Errorf("asked %v, want %v", got.Asked, want)
	}
}

func TestATasksPageSinceNamesTheUnitsUntilItIsRead(t *testing.T) {
	var got struct {
		Asked []string `json:"asked"`
	}
	nodeRun(t, viewsPrelude(t), nil, `
const asked = [];
let fail = false;
async function getJSON(u) {
  asked.push(u);
  if (u === "/api/views") return input.cat;
  if (fail) throw new Error("down");
  return { epoch: "ep", rev: 1, full: !u.includes("since"), tasks: [], removed: [], counters: { views: {} } };
}
(async () => {
  const route = { view: "inbox", area: "", ctx: [] };
  await loadTasks(route);
  fail = true;
  await loadTasks(route, ["github:a/b#1"]);
  fail = false;
  await loadTasks(route, ["slack:T1/C1/1"]);
  await loadTasks(route);
  process.stdout.write(JSON.stringify({ asked }));
})();`, map[string]any{"cat": viewsCatalog()}, &got)
	held := "/api/tasks?view=inbox&since=ep%3A1&units=github%3Aa%2Fb%231&units=slack%3AT1%2FC1%2F1"
	want := []string{"/api/views", "/api/tasks?view=inbox", "/api/tasks?view=inbox&since=ep%3A1&units=github%3Aa%2Fb%231", held, "/api/tasks?view=inbox&since=ep%3A1"}
	if !slices.Equal(got.Asked, want) {
		t.Errorf("asked %v, want %v", got.Asked, want)
	}
}

func TestAReconnectReadsTheTasksWhole(t *testing.T) {
	got := runTasksDelta(t, `
  pages[since("e1", 1)] = { epoch: "e1", rev: 1, full: false, rows: [], removed: [] };
  sources[0].onopen();
  await flush();`)
	if i := slices.Index(got, "forget"); i < 0 || i > slices.Index(got, "tasks ") {
		t.Errorf("requests %v, want the tasks page forgotten, then the tasks read", got)
	}
}
