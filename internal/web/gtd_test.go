package web

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/domain/unit"
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

// gtdCatalog is GET /api/gtd with the core views the daemon serves.
func gtdCatalog() api.GTDCatalog {
	return api.GTDCatalog{
		Views: []api.GTDView{{ID: "focus", Label: "Focus"}, {ID: "inbox", Label: "Inbox"}, {ID: "outbox", Label: "Outbox"}, {ID: "next", Label: "Next"},
			{ID: "waiting", Label: "Waiting"}, {ID: "projects", Label: "Projects"}, {ID: "someday", Label: "Someday"}, {ID: "logbook", Label: "Logbook"}},
		Areas:    []string{"OSS", "Hiring"},
		Contexts: []string{"@decide", "@reply"},
		Verbs: []api.GTDVerb{{Verb: "approve", Label: "Approve"}, {Verb: "edit", Label: "Edit"}, {Verb: "dismiss", Label: "Discard"},
			{Verb: "retry", Label: "Retry"}},
	}
}

var coreViewIDs = []string{"focus", "inbox", "outbox", "next", "waiting", "projects", "someday", "logbook"}

// noGTD stands in for gtd.js where a test runs app.js's loading alone.
const noGTD = "const loadGTD = async () => {};\n"

// noUpdate stands in for update.js where a test runs app.js's loading alone.
const noUpdate = "const checkBuild = () => {};\n"

// gtdScripts are routes.js, gtd.js and detail.js.
func gtdScripts(t *testing.T) string {
	return staticJS(t, "routes.js") + staticJS(t, "gtd.js") + staticJS(t, "detail.js")
}

// gtdPrelude is plugins.js, routes.js, gtd.js and detail.js over a tree h.
func gtdPrelude(t *testing.T) string {
	return treeJS + pluginsJS(t) + gtdScripts(t)
}

func entry(id, state string, mod func(*api.EntryView)) api.EntryView {
	e := api.EntryView{ID: id, Kind: "action", State: state, Title: "Entry " + id, Contexts: []string{}, Units: []unit.Unit{}, Items: []api.ItemView{}, Verbs: []api.VerbState{},
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
	nodeRun(t, catalogPrelude(t)+gtdPrelude(t), nil, `
const nav = gtdNav(input.cat, catalog.views, ["fake-board"], input.counters);
process.stdout.write(JSON.stringify({ ids: nav.map((v) => v.id), core: nav.map((v) => v.core), counts: nav.filter((v) => v.core).map((v) => v.count) }));`,
		map[string]any{"cat": gtdCatalog(), "counters": counters}, &got)
	plugin := []string{"needs-me", "stalled", "mine", "loop", "plan", "approved", "dash", "done", "all", "explore", "recent"}
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
	nodeRun(t, catalogPrelude(t)+gtdPrelude(t), nil, `
const served = catalog.views;
const nav = gtdNav(input.cat, [], [], null);
process.stdout.write(JSON.stringify({
  ids: nav.map((v) => v.id),
  view: gtdResolveView("needs-me", input.cat, [], () => false),
  before: gtdResolveView("needs-me", null, [], () => false),
  enabled: gtdResolveView("needs-me", input.cat, served, () => false),
  core_stay: gtdResolveView("waiting", input.cat, [], () => false),
}));`, map[string]any{"cat": gtdCatalog()}, &got)
	if !slices.Equal(got.IDs, coreViewIDs) {
		t.Errorf("nav with the GitHub plugin off %v, want the core views alone", got.IDs)
	}
	if got.View != "focus" {
		t.Errorf("#view=needs-me with the plugin off shows %q, want focus", got.View)
	}
	if got.Before != "needs-me" || got.Enabled != "needs-me" || got.CoreStay != "waiting" {
		t.Errorf("before load %q, plugin on %q, core %q", got.Before, got.Enabled, got.CoreStay)
	}
}

func TestGroupingPerView(t *testing.T) {
	entries := []api.EntryView{
		entry("e-1", "next", func(e *api.EntryView) { e.Contexts = []string{"@reply"} }),
		entry("e-2", "next", func(e *api.EntryView) { e.Contexts = []string{"@decide", "@reply"} }),
		entry("e-3", "next", nil),
		entry("e-4", "next", func(e *api.EntryView) { e.Contexts = []string{"@reply"} }),
	}
	waiting := []api.EntryView{
		entry("w-1", "waiting", func(e *api.EntryView) { e.Waiting = &api.WaitingView{On: []api.WaitingPerson{{Name: "bob"}}} }),
		entry("w-2", "waiting", func(e *api.EntryView) { e.Waiting = &api.WaitingView{On: []api.WaitingPerson{{Name: "alice"}}} }),
		entry("w-3", "waiting", func(e *api.EntryView) { e.Waiting = &api.WaitingView{On: []api.WaitingPerson{{Name: "bob"}}} }),
	}
	logbook := []api.EntryView{entry("d-1", "done", nil), entry("r-1", "reference", nil), entry("d-2", "done", nil)}
	type group struct {
		Label string   `json:"label"`
		IDs   []string `json:"ids"`
	}
	var got map[string][]group
	nodeRun(t, gtdPrelude(t), nil, `
const g = (v, es) => gtdGroups(v, es).map((x) => ({ label: x.label, ids: x.entries.map((e) => e.id) }));
process.stdout.write(JSON.stringify({ next: g("next", input.next), waiting: g("waiting", input.waiting), logbook: g("logbook", input.logbook), inbox: g("inbox", input.next) }));`,
		map[string]any{"next": entries, "waiting": waiting, "logbook": logbook}, &got)
	want := map[string][]group{
		"next":    {{"@decide", []string{"e-2"}}, {"@reply", []string{"e-1", "e-4"}}, {"No context", []string{"e-3"}}},
		"waiting": {{"alice", []string{"w-2"}}, {"bob", []string{"w-1", "w-3"}}},
		"logbook": {{"Reference", []string{"r-1"}}, {"Done", []string{"d-1", "d-2"}}},
		"inbox":   {{"", []string{"e-1", "e-2", "e-3", "e-4"}}},
	}
	for v, w := range want {
		if !slices.EqualFunc(got[v], w, func(a, b group) bool { return a.Label == b.Label && slices.Equal(a.IDs, b.IDs) }) {
			t.Errorf("%s groups %+v, want %+v", v, got[v], w)
		}
	}
}

func TestNavCountsComeFromThePage(t *testing.T) {
	full := api.EntriesPage{Epoch: "ep", Rev: 4, Full: true, Removed: []string{},
		Entries:  []api.EntryView{entry("e-1", "inbox", nil), entry("e-2", "inbox", nil)},
		Counters: api.Counters{Views: map[string]int{"inbox": 2}}}
	delta := api.EntriesPage{Epoch: "ep", Rev: 6, Removed: []string{"e-1"}, Entries: []api.EntryView{},
		Counters: api.Counters{Views: map[string]int{"inbox": 1}}}
	var got struct {
		Asked  []string `json:"asked"`
		Counts []any    `json:"counts"`
		Lens   []int    `json:"lens"`
	}
	nodeRun(t, gtdPrelude(t), nil, `
const asked = []; const pages = [input.full, input.delta];
async function getJSON(u) { asked.push(u); return u === "/api/gtd" ? input.cat : pages.shift(); }
const inboxCount = () => gtdNav(gtdState.catalog, [], [], gtdState.counters).find((v) => v.id === "inbox").count;
(async () => {
  const route = { view: "inbox", area: "", ctx: [] };
  const counts = [], lens = [];
  await loadGTD(route);
  counts.push(inboxCount()); lens.push(gtdState.entries.length);
  // A change carries other counters; the list reloads and its page wins.
  const change = { counters: { views: { inbox: 3 } } };
  if (change.counters) await loadGTD(route);
  counts.push(inboxCount()); lens.push(gtdState.entries.length);
  process.stdout.write(JSON.stringify({ asked, counts, lens }));
})();`, map[string]any{"cat": gtdCatalog(), "full": full, "delta": delta}, &got)
	if len(got.Counts) != 2 || got.Counts[0] != float64(2) || got.Counts[1] != float64(1) || !slices.Equal(got.Lens, []int{2, 1}) {
		t.Errorf("counts %v with lists of %v, want the page's 2 then 1", got.Counts, got.Lens)
	}
	if want := []string{"/api/gtd", "/api/entries?view=inbox", "/api/entries?view=inbox&since=ep%3A4"}; !slices.Equal(got.Asked, want) {
		t.Errorf("asked %v, want %v", got.Asked, want)
	}
}

func TestAreaSparesInbox(t *testing.T) {
	var got struct {
		URLs  map[string]string `json:"urls"`
		Keys  []string          `json:"keys"`
		Route map[string]any    `json:"route"`
		Hash  string            `json:"hash"`
		Asked []string          `json:"asked"`
	}
	nodeRun(t, gtdPrelude(t), nil, `
const urls = {};
for (const v of input.views) urls[v] = entriesURL(v, "OSS", [], "");
const asked = [];
async function getJSON(u) { asked.push(u); return u === "/api/gtd" ? input.cat : { epoch: "e", rev: 1, full: true, entries: [], removed: [], counters: { views: {} } }; }
(async () => {
  await loadGTD({ view: "inbox", area: "OSS", ctx: [] });
  await loadGTD({ view: "focus", area: "OSS", ctx: [] });
  await loadGTD({ view: "needs-me", area: "OSS", ctx: [] });
  process.stdout.write(JSON.stringify({
    urls, asked,
    keys: [gtdQueryKey("inbox", "OSS", []), gtdQueryKey("inbox", "", []), gtdQueryKey("next", "OSS", [])],
    route: readRoute(routeHash("next", "", new URLSearchParams({ area: "OSS" }))),
    hash: routeHash("next", "", new URLSearchParams({ area: "OSS" })),
  }));
})();`, map[string]any{"cat": gtdCatalog(), "views": coreViewIDs}, &got)
	for _, v := range coreViewIDs {
		has := strings.Contains(got.URLs[v], "area=OSS")
		if has == (v == "inbox") {
			t.Errorf("%s: %s", v, got.URLs[v])
		}
	}
	if got.Keys[0] != got.Keys[1] || got.Keys[2] == "next||" {
		t.Errorf("query keys %v: Inbox must not depend on the area", got.Keys)
	}
	if got.Route["area"] != "OSS" || got.Hash != "view=next&area=OSS" {
		t.Errorf("the area is not kept in the hash: %q -> %v", got.Hash, got.Route)
	}
	if want := []string{"/api/gtd", "/api/entries?view=inbox", "/api/counters?area=OSS", "/api/entries?view=focus&area=OSS", "/api/counters?area=OSS"}; !slices.Equal(got.Asked, want) {
		t.Errorf("asked %v, want %v", got.Asked, want)
	}
}

func TestFakeProviderRowRenders(t *testing.T) {
	src := appJS(t)
	prelude := lensPrelude(t) + jsConst(t, src, "focusSlot") + jsConst(t, src, "CARD_GROUPS") + jsConst(t, src, "STRIP_TITLE") + jsConst(t, src, "TAG_CHIPS") + treeJS +
		`state.busy = new Set(); state.openSlot = ""; state.specs = { version: "v", byId: {} };
const CSS = { escape: (s) => s }; const document = { querySelector: () => null };
const providerName = (id) => id;
`
	row := map[string]any{
		"unit": map[string]string{"provider": "fakethread", "key": "T1/C1"}, "label": "#general thread", "title": "Can someone look at the deploy?",
		"link": "https://example.invalid/T1/C1", "kind": "waiting", "views": []string{"all"}, "actions": []any{},
		"summary": map[string]any{"waiting_on": "me", "reason": "a question waits on you"},
	}
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

func outboxEntries() []api.EntryView {
	return []api.EntryView{
		entry("draft:7", "inbox", func(e *api.EntryView) {
			e.Kind, e.Title = "draft", "Reply to the deploy thread"
			e.Draft = &api.Draft{ID: 7, Unit: "slack/T1/C1", Kind: "reply", Target: "C1", Where: `#deploys in the thread "Deploy at five"`, Text: "Deployed, all green.", State: "proposed", Rev: 3,
				Would: api.DraftWould{Policy: "release", Reason: "a reply from a turn"}}
			e.Verbs = []api.VerbState{{Verb: "approve", Enabled: true}, {Verb: "edit", Enabled: true}, {Verb: "dismiss", Enabled: true}, {Verb: "retry", Enabled: false}}
		}),
		entry("draft:8", "inbox", func(e *api.EntryView) {
			e.Kind, e.Title = "draft", "Nudge the reviewer"
			e.Draft = &api.Draft{ID: 8, Unit: "github/acme/api#8", Kind: "nudge", Target: "acme/api#8", Text: "Ping?", State: "failed", Error: "rate limited", Rev: 5,
				Would: api.DraftWould{Policy: "hold", Reason: "low confidence"}}
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
	nodeRun(t, gtdPrelude(t), nil, `
const posts = [];
detailEnv.post = async (path, body) => { posts.push({ path, body }); return { entry: {} }; };
detailEnv.notice = () => {}; detailEnv.changed = () => {}; detailEnv.render = () => {};
function writeHash() {}
gtdState.catalog = input.cat;
const route = { view: "outbox", area: "", ctx: [], entry: "" };
gtdState.key = gtdQueryKey("outbox", "", []);
gtdState.entries = input.entries;
const list = () => gtdList(route, "");
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
})();`, map[string]any{"cat": gtdCatalog(), "entries": outboxEntries()}, &got)
	for _, s := range []string{`To #deploys in the thread "Deploy at five"`, "Deployed, all green.", "Auto-send would release: a reply from a turn",
		"To acme/api#8 (destination unknown)", "Ping?", "rate limited", "Auto-send would hold: low confidence"} {
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
		{"/api/entries/draft:7/approve", `{"args":{"rev":"3"},"source":"cockpit"}`},
		{"/api/entries/draft:7/approve", `{"args":{"rev":"3","text":"Deployed; two checks were retried."},"source":"cockpit"}`},
		{"/api/entries/draft:8/dismiss", `{"source":"cockpit"}`},
		{"/api/entries/draft:8/retry", `{"args":{"rev":"5"},"source":"cockpit"}`},
	}
	if !slices.Equal(posts, want) {
		t.Errorf("posts %v, want %v", posts, want)
	}
}

func TestUnitLabelFallsBackToTheUnitForEveryProvider(t *testing.T) {
	var got []string
	nodeRun(t, gtdPrelude(t), nil, `process.stdout.write(JSON.stringify([
  unitLabel({ provider: "github", key: "acme/api#7" }), unitLabel({ provider: "linear", key: "PAY-88" }), unitLabel(null)]));`, nil, &got)
	if want := []string{"github:acme/api#7", "linear:PAY-88", ""}; !slices.Equal(got, want) {
		t.Fatalf("labels %q, want %q", got, want)
	}
}
