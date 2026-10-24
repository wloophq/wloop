package web

import (
	"slices"
	"strings"
	"testing"
)

const deltaPrelude = `const state = { specs: { version: "", byId: {} }, rows: [], epoch: "", rev: 0, detail: null, health: null, error: "", busy: new Set(), conn: "" };
const API_VERSION = "1";
const requests = [], timers = [], sources = [];
let pending, queued = null, chain = Promise.resolve(), retries = 0, opened = false, hash = { view: "needs-me", pr: "" };
const setTimeout = (f) => timers.push(f);
const clearTimeout = () => {};
const readHash = () => hash;
const render = () => {}, refreshSpecs = async () => {}, loadExploreRecent = async () => {}, loadPlan = async () => {};
const applyCatalog = () => {};
const loadActivity = async () => { requests.push("activity"); };
const loadDetail = async () => { requests.push("detail"); };
class EventSource { constructor() { sources.push(this); } }
EventSource.CLOSED = 2;
const unit = (k) => ({ provider: "github", key: k });
const row = (k, title) => ({ unit: unit(k), label: k, title });
let pages = {};
` + noPlugins + noGTD + `const getJSON = async (url) => {
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
	want := []string{"/api/rows?since=e1%3A1", "/api/health"}
	if !slices.Equal(got.Requests, want) {
		t.Errorf("requests %v, want %v", got.Requests, want)
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
