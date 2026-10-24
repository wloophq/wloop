package web

import (
	"encoding/json"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/api"
)

func TestAppJSHasNoProviderTables(t *testing.T) {
	src := appJS(t)
	for _, gone := range []string{"const VIEWS", "const KIND_LABEL", "const FIELDS", "const KIND_ORDER", "const WAIT_LABEL", "const OPENED"} {
		if strings.Contains(src, gone) {
			t.Errorf("app.js still declares %s", gone)
		}
	}
	if m := regexp.MustCompile(`values: \(r\) => \[?r\.`).FindString(src); m != "" {
		t.Errorf("app.js computes a facet's values from the row: %s", m)
	}
}

func TestAPIVersionMatchesTheWeb(t *testing.T) {
	if got, want := jsConst(t, appJS(t), "API_VERSION"), `const API_VERSION = "`+api.Version+`";`+"\n"; got != want {
		t.Errorf("app.js has %q, want %q", got, want)
	}
}

func TestTimeFacetsBucketOnTheClient(t *testing.T) {
	day := 24 * time.Hour
	t0 := time.Date(2026, 10, 14, 15, 0, 0, 0, time.UTC)
	row := map[string]any{"unit": map[string]any{"provider": "github", "key": "acme/api#1"}, "kind": "review",
		"facets": map[string][]string{"opened": {iso(t0.Add(-20 * time.Hour))}, "age": {iso(t0.Add(-2 * time.Hour))},
			"due": {iso(t0.Add(30 * time.Hour))}, "is": {"needs-me"}}}
	var got []map[string][]string
	nodeRun(t, keyJS(t)+catalogPrelude(t)+jsConst(t, appJS(t), "rowValuesCache"), []string{"fold", "fieldById", "rowValues", "rowKey"}, `
const r = input.row;
const at = (ms) => { Date.now = () => ms; return Object.fromEntries(["opened", "age", "due", "is"].map((id) => [id, rowValues(fieldById(id), r)])); };
process.stdout.write(JSON.stringify([at(input.t0), at(input.t0 + 2 * 86400000)]));`,
		map[string]any{"row": row, "t0": t0.UnixMilli()}, &got)
	want := []map[string][]string{
		{"opened": {"1d"}, "age": {"1d"}, "due": {"later"}, "is": {"needs-me"}},
		{"opened": {"3d"}, "age": {"3d"}, "due": {"past"}, "is": {"needs-me"}},
	}
	if mustJS(t, got) != mustJS(t, want) {
		t.Errorf("got  %v\nwant %v", got, want)
	}
	row["facets"].(map[string][]string)["due"] = []string{iso(t0.Add(day + 6*time.Hour))}
	got = nil
	nodeRun(t, keyJS(t)+catalogPrelude(t)+jsConst(t, appJS(t), "rowValuesCache"), []string{"fold", "fieldById", "rowValues", "rowKey"}, `
const r = input.row;
const at = (ms) => { Date.now = () => ms; return { due: rowValues(fieldById("due"), r), is: rowValues(fieldById("is"), r) }; };
process.stdout.write(JSON.stringify([at(input.t0), at(input.t0 + 86400000)]));`,
		map[string]any{"row": row, "t0": t0.UnixMilli()}, &got)
	want = []map[string][]string{{"due": {"later"}, "is": {"needs-me"}}, {"due": {"due"}, "is": {"needs-me", "due"}}}
	if mustJS(t, got) != mustJS(t, want) {
		t.Errorf("due a day later: got %v, want %v", got, want)
	}
}

// domShim is a minimal DOM: elements that keep their children and text,
// and stubs for what app.js touches while it loads.
const domShim = `
class Node {}
class Text extends Node { constructor(t) { super(); this.data = t; } get textContent() { return this.data; } }
class El extends Node {
  constructor(tag) { super(); this.tag = tag; this.children = []; this.attrs = {}; this.dataset = {}; this.style = { setProperty() {} }; this.hidden = false;
    this.classList = { add() {}, remove() {}, toggle() {}, contains: () => false }; }
  append(...c) { this.children.push(...c.map((x) => (x instanceof Node ? x : new Text(String(x))))); }
  replaceChildren(...c) { this.children = []; this.append(...c); }
  setAttribute(k, v) { this.attrs[k] = v; }
  getAttribute(k) { return this.attrs[k]; }
  addEventListener() {}
  querySelector() { return null; }
  querySelectorAll() { return []; }
  closest() { return null; }
  replaceWith() {}
  focus() {}
  scrollIntoView() {}
  setSelectionRange() {}
  get className() { return this.attrs.class || ""; }
  set className(v) { this.attrs.class = v; }
  get textContent() { return this.children.map((c) => c.textContent).join(""); }
  set textContent(v) { this.children = [new Text(String(v))]; }
}
class HTMLInputElement extends El {}
class HTMLTextAreaElement extends El {}
class HTMLSelectElement extends El {}
const els = {};
const document = { createElement: (t) => new El(t), createTextNode: (t) => new Text(t), getElementById: (id) => els[id] || (els[id] = new El("div")),
  querySelector: () => new El("div"), querySelectorAll: () => [], addEventListener() {}, body: new El("body"), documentElement: new El("html"), activeElement: null };
const window = { addEventListener() {} };
const location = { hash: "" };
const history = { replaceState() {} };
const matchMedia = () => ({ matches: false, addEventListener() {} });
const localStorage = { getItem: () => null, setItem() {} };
const CSS = { escape: (s) => s };
const navigator = {};
const setInterval = () => 0;
`

// cockpitScript is dash.js and app.js up to the listeners and the boot,
// over domShim.
func cockpitScript(t *testing.T) string {
	src := cockpitJS(t)
	app := appJS(t)
	boot := strings.Index(app, "\nwindow.addEventListener(\"hashchange\"")
	if boot < 0 {
		t.Fatal("app.js has no hashchange listener")
	}
	raw := strings.TrimPrefix(src, app) + "\n" + app[:boot]
	return domShim + pluginsJS(t) + gtdScripts(t) + strings.Replace(raw, `"use strict";`, "", 1)
}

func TestThreadRowRendersInTheCockpit(t *testing.T) {
	now := time.Date(2026, 10, 14, 15, 0, 0, 0, time.UTC)
	var catalog map[string]any
	if err := json.Unmarshal([]byte(servedCatalog(t)), &catalog); err != nil {
		t.Fatal(err)
	}
	catalog["situations"] = append(catalog["situations"].([]any), map[string]any{"provider": "thread", "kind": "waiting", "label": "waits for a reply", "rank": 0})
	row := map[string]any{"unit": map[string]any{"provider": "thread", "key": "c1"}, "link": "https://chat.example/c1", "label": "#general c1", "title": "Deploy question",
		"kind": "waiting", "summary": map[string]any{"waits_on": "ana", "since": iso(now.Add(-3 * time.Hour)), "stalled": false, "closed": false,
			"reason": "asked about the deploy", "line": "ana asked about the deploy"},
		"views": []string{"needs-me"}, "facets": map[string][]string{}, "actions": []any{}, "specs_version": ""}
	var got map[string]string
	nodeRun(t, cockpitScript(t), nil, `
Date.now = () => input.now;
applyCatalog(input.catalog);
state.health = { viewer: "me" };
const r = input.row;
state.rows = [r];
const lens = { view: "needs-me", q: "", ts: [], quals: [], filters: new Map(), group: "", sort: "", layout: "" };
const tr = rowTr(r, "", lens);
const c = card(r, "");
location.hash = "#" + routeHash("needs-me", keyOf(r));
state.detail = { row: r, events: [] };
renderDetail();
const shown = pipeline(state.rows, lens).shown;
process.stdout.write(JSON.stringify({ row: tr.textContent, card: c.textContent, detail: document.getElementById("detail").textContent, shown: String(shown) }));`,
		map[string]any{"row": row, "catalog": catalog, "now": now.UnixMilli()}, &got)
	for _, part := range []string{"row", "card", "detail"} {
		for _, want := range []string{"Deploy question", "#general c1", "waits for a reply", "ana", "3h 0m"} {
			if !strings.Contains(got[part], want) {
				t.Errorf("%s lacks %q: %s", part, want, got[part])
			}
		}
	}
	for _, part := range []string{"row", "detail"} {
		if !strings.Contains(got[part], "asked about the deploy") {
			t.Errorf("%s lacks the reason: %s", part, got[part])
		}
	}
	if !strings.Contains(got["detail"], "ana asked about the deploy") || !strings.Contains(got["detail"], "since ") {
		t.Errorf("detail lacks the summary line or since: %s", got["detail"])
	}
	if got["shown"] != "1" {
		t.Errorf("the needs-me list shows %s rows", got["shown"])
	}
}

// lensOp is a filter, group or sort of the cockpit's lens with what app.js
// shows for the deriver fixtures (testdata/lens.golden.json).
type lensOp struct {
	View   string          `json:"view,omitempty"`
	Q      string          `json:"q,omitempty"`
	Group  string          `json:"group,omitempty"`
	Sort   string          `json:"sort,omitempty"`
	Layout string          `json:"layout,omitempty"`
	Result json.RawMessage `json:"result"`
}

func TestLensFromServedFacetsMatchesTheGolden(t *testing.T) {
	var ops []lensOp
	if err := json.Unmarshal(readFile(t, "testdata/lens.golden.json"), &ops); err != nil {
		t.Fatal(err)
	}
	var rows []any
	if err := json.Unmarshal(readFile(t, "../readmodel/prrow/testdata/rows.json"), &rows); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 14, 15, 0, 0, 0, time.UTC)
	var got []json.RawMessage
	nodeRun(t, lensPrelude(t), lensFuncs, `
Date.now = () => input.now;
state.health.viewer = "me-login";
process.stdout.write(JSON.stringify(input.ops.map((o) => {
  const q = o.q || "";
  const lens = { view: o.view || "all", q, ts: terms(q), quals: qualifiers(q), filters: filtersOf(qualifiers(q)), group: o.group || "", sort: o.sort || "", layout: o.layout || "", done: false };
  const r = pipeline(input.rows, lens);
  return { group: r.group, total: r.total, shown: r.shown, groups: r.groups.map((g) => ({ key: g.key, label: g.label, rows: g.rows.map((x) => x.label) })) };
})));`, map[string]any{"ops": ops, "rows": rows, "now": now.UnixMilli()}, &got)
	if len(got) != len(ops) {
		t.Fatalf("%d results for %d ops", len(got), len(ops))
	}
	for i, op := range ops {
		if !jsonEqual(t, got[i], op.Result) {
			t.Errorf("view %q q %q group %q sort %q layout %q:\n got  %s\n want %s", op.View, op.Q, op.Group, op.Sort, op.Layout, got[i], op.Result)
		}
	}
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func jsonEqual(t *testing.T, a, b []byte) bool {
	t.Helper()
	var x, y any
	if err := json.Unmarshal(a, &x); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &y); err != nil {
		t.Fatal(err)
	}
	return mustJS(t, x) == mustJS(t, y)
}

func TestDeepLinkViewSurvivesBeforeTheCatalog(t *testing.T) {
	src := appJS(t)
	prelude := jsConst(t, src, "catalog") + jsConst(t, src, "UNIT_KEY") + jsConst(t, src, "QUERY_MAX") +
		noPlugins + "const location = { hash: \"#view=stalled&q=x\" };\nconst groupField = () => null;\nconst parseSort = () => ({ field: \"\" });\n" + gtdScripts(t)
	var got struct{ Before, Unknown, After, Missing string }
	nodeRun(t, prelude, []string{"readHash"}, `
const before = readHash().view;
catalog.views = [{ id: "needs-me" }, { id: "stalled" }];
const after = readHash().view;
location.hash = "#view=nope";
const unknown = readHash().view;
location.hash = "";
const missing = readHash().view;
process.stdout.write(JSON.stringify({ before, unknown, after, missing }));
`, nil, &got)
	if got.Before != "stalled" || got.After != "stalled" || got.Unknown != "needs-me" || got.Missing != "needs-me" {
		t.Fatalf("%+v", got)
	}
}
