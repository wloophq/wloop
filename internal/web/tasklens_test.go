package web

import (
	"encoding/json"
	"maps"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/api/lens"
	"github.com/wloophq/wloop/internal/catalog"
	"github.com/wloophq/wloop/internal/domain/unit"
	intentload "github.com/wloophq/wloop/internal/intent"
	psource "github.com/wloophq/wloop/internal/plugins/source"
)

// servedViews is GET /api/views as the daemon serves the bundled profile: its views, its task facets and what
// the sort menu calls a view's own order.
func servedViews(t *testing.T) api.ViewsCatalog {
	t.Helper()
	def, problems := intentload.BundledViews(os.DirFS("../../profiles/default"))
	if len(problems) > 0 {
		t.Fatal(problems)
	}
	cat := api.ViewsCatalog{Facets: catalog.TaskFacets(def.Facets), DefaultSort: def.DefaultSort}
	for _, v := range def.Views {
		cat.Views = append(cat.Views, api.TaskViewSpec{ID: v.ID, Label: v.Label, Summary: v.Summary, Empty: v.Empty, When: v.WhenText, Sort: v.SortText, Over: v.Over, Group: v.Group})
	}
	return cat
}

// slackFacets are the Slack plugin's facets the GitHub plugin does not declare too.
func slackFacets(t *testing.T) []api.FacetSpec {
	t.Helper()
	var github struct {
		Facets []api.FacetSpec `json:"facets"`
	}
	if err := json.Unmarshal([]byte(servedCatalog(t)), &github); err != nil {
		t.Fatal(err)
	}
	var out []api.FacetSpec
	for _, f := range psource.BundledCatalog("slack").Facets() {
		if !slices.ContainsFunc(github.Facets, func(g api.FacetSpec) bool { return g.ID == f.ID }) {
			out = append(out, f)
		}
	}
	return out
}

// taskSpecs are the specs the daemon counts a task query with: the plugins' facets, then the profile's, by id.
func taskSpecs(t *testing.T, views api.ViewsCatalog) map[string]api.FacetSpec {
	t.Helper()
	var github struct {
		Facets []api.FacetSpec `json:"facets"`
	}
	if err := json.Unmarshal([]byte(servedCatalog(t)), &github); err != nil {
		t.Fatal(err)
	}
	specs := map[string]api.FacetSpec{}
	for _, f := range slices.Concat(github.Facets, slackFacets(t), views.Facets) {
		specs[f.ID] = f
	}
	return specs
}

var prUnit2 = unit.Unit{Provider: unit.GitHub, Key: "acme/web#9"}

// mixedTasks are Needs me's tasks: two Slack conversations and two pull requests, with the facets the daemon
// serves on each.
func mixedTasks() []api.TaskView {
	return []api.TaskView{
		task("s-1", "next", func(e *api.TaskView) {
			e.Title, e.Category, e.Units, e.UnitLabels = "Answer the deploy question", "ready", []unit.Unit{slackUnit}, []string{"#general thread"}
			e.Facets = map[string][]string{"source": {"slack"}, "where": {"general"}, "conversation": {"thread"}, "waits-on": {"me"}, "state": {"next"}, "category": {"ready"}}
		}),
		task("s-2", "next", func(e *api.TaskView) {
			e.Title, e.Category = "Close the lunch poll", "ready"
			e.Facets = map[string][]string{"source": {"slack"}, "where": {"random"}, "waits-on": {"me"}, "state": {"next"}, "category": {"ready"}}
		}),
		task("g-1", "doing", func(e *api.TaskView) {
			e.Title, e.Category, e.Units = "Fix the flaky upload test", "active", []unit.Unit{prUnit}
			e.Facets = map[string][]string{"source": {"github"}, "where": {"acme/api"}, "ci": {"failure"}, "kind": {"checks"}, "wait": {"me"}, "waits-on": {"me"},
				"state": {"doing"}, "category": {"active"}, "tag": {"lang:go"}}
		}),
		task("g-2", "doing", func(e *api.TaskView) {
			e.Title, e.Category, e.Units = "Review the general settings page", "active", []unit.Unit{prUnit2}
			e.Facets = map[string][]string{"source": {"github"}, "where": {"acme/web"}, "ci": {"success"}, "kind": {"review"}, "wait": {"reviewer"}, "waits-on": {"bia"},
				"state": {"doing"}, "category": {"active"}}
		}),
	}
}

// taskLensFuncs are app.js's lens functions a task view runs, with what they call.
var taskLensFuncs = []string{"tokens", "unquote", "terms", "fieldById", "tagKeyField", "groupField", "tagKeys", "normValue", "qualifierOf", "qualifiers", "qualifierToken",
	"toggledQualifier", "withoutQualifiers", "haystack", "matches", "itemValues", "itemKey", "filtersOf", "passesFilters", "parseSort", "lensFields", "fieldsFor",
	"viewSource", "offeredFields", "viewGroupField", "defaultSortLabel", "lensOf", "lensPicks", "effectiveGroup", "cmp", "keyRank", "keyOrder", "compareItems",
	"groupLabel", "groupItems", "pipelineOf", "groupedRows", "groupQualifier", "onlyThis", "multiCopy", "renderLens", "hideLens", "lensBar", "lensSelect",
	"lensChips", "chip", "stripLegend", "waitField", "setGroup", "clearLens", "copyList", "lensResult", "sortLabel", "listYAML", "copyEntry", "yamlString", "inView"}

// taskLensJS declares the served GitHub catalog and the lens functions views.js calls, over a state with no rows.
func taskLensJS(t *testing.T) string {
	src := appJS(t)
	var b strings.Builder
	b.WriteString(catalogPrelude(t))
	for _, c := range []string{"folded", "itemValuesCache", "NO_GROUP", "lensMemo", "labelOf"} {
		b.WriteString(jsConst(t, src, c))
	}
	b.WriteString("const state = { rows: [], loaded: false, health: null };\n")
	for _, f := range taskLensFuncs {
		b.WriteString(jsFunction(t, src, f))
	}
	b.WriteString("const taskListNodes = (r, open) => { const p = viewPage(r, open); return [...p.head, ...(p.res ? lensBar(p.lens, p.res) : []), ...p.body]; };\n")
	return b.String()
}

// mixedPrelude loads the bundled views, the GitHub and Slack facets and Needs me's tasks.
func mixedPrelude(t *testing.T) string {
	return viewsPrelude(t) + "\nfunction writeHash() {}\n" + `
catalog.fields.push(...input.slack.map(fieldOf));
tasksState.catalog = input.cat;
tasksState.key = viewQueryKey("needs-me", "", []);
tasksState.tasks = input.tasks;
const route = (r) => ({ view: "needs-me", area: "", ctx: [], task: "", q: "", ...r });
const options = (bar, slot) => byFocus(bar, slot).children.map((o) => o.attrs.value);
`
}

func mixedInput(t *testing.T, extra map[string]any) map[string]any {
	in := map[string]any{"cat": servedViews(t), "slack": slackFacets(t), "tasks": mixedTasks()}
	for k, v := range extra {
		in[k] = v
	}
	return in
}

func TestNeedsMeOffersTheFieldsItsTasksHave(t *testing.T) {
	rows := wireRows("me", time.Now(), lensRows()...)
	for _, r := range rows {
		r["views"] = []string{"mine"}
	}
	var got struct {
		Group, Sort, Mine []string
	}
	nodeRun(t, mixedPrelude(t), nil, `
const p = viewPage(route({}), "");
const bar = lensBar(p.lens, p.res);
process.stdout.write(JSON.stringify({ Group: options(bar, "lens:group"), Sort: options(bar, "lens:sort"), Mine: fieldsFor("mine", input.rows).map((f) => f.id) }));`,
		mixedInput(t, map[string]any{"rows": rows}), &got)
	for _, id := range []string{"where", "ci", "source", "conversation", "kind", "state", "category", "tag:lang"} {
		if !slices.Contains(got.Group, id) {
			t.Errorf("Needs me's Group menu %v lacks %s", got.Group, id)
		}
	}
	for _, id := range []string{"opened", "author", "asked-by", "placement", "moved"} {
		if slices.Contains(got.Group, id) || slices.Contains(got.Sort, id) {
			t.Errorf("Needs me's menus offer %s, which none of its tasks has: group %v sort %v", id, got.Group, got.Sort)
		}
	}
	if !slices.Contains(got.Sort, "source") || got.Sort[0] != "" {
		t.Errorf("Needs me's Sort menu %v", got.Sort)
	}
	for _, id := range []string{"conversation", "source", "state", "category", "waits-on"} {
		if id != "waits-on" && slices.Contains(got.Mine, id) {
			t.Errorf("My PRs offers %s: %v", id, got.Mine)
		}
	}
	if !slices.Contains(got.Mine, "where") || !slices.Contains(got.Mine, "author") {
		t.Errorf("My PRs offers %v", got.Mine)
	}
}

func TestATaskViewGroupsBySourceAndFilters(t *testing.T) {
	type group struct {
		Key, Label string
		IDs        []string
	}
	var got struct {
		Group  string
		Groups []group
		Text   string
		Count  string
		Nested bool
	}
	nodeRun(t, mixedPrelude(t), nil, `
const p = viewPage(route({ q: "where:general", g: "source" }), "");
const body = h("div", {}, ...p.body);
process.stdout.write(JSON.stringify({ Group: p.res.group, Groups: p.res.groups.filter((g) => g.rows.length).map((g) => ({ Key: g.key, Label: g.label, IDs: g.rows.map((it) => it.task.id) })),
  Text: text(body), Nested: p.body.some(Array.isArray), Count: text(byClass(lensBar(p.lens, p.res), "lens-count")) }));`, mixedInput(t, nil), &got)
	if got.Group != "source" || len(got.Groups) != 1 || got.Groups[0].Key != "slack" || !slices.Equal(got.Groups[0].IDs, []string{"s-1"}) {
		t.Fatalf("where:general grouped by source: %+v", got)
	}
	if !strings.Contains(got.Text, "slack (1)") || !strings.Contains(got.Text, "Answer the deploy question") || strings.Contains(got.Text, "general settings") {
		t.Errorf("list %q", got.Text)
	}
	if got.Nested {
		t.Error("the page body holds a list of nodes where the DOM takes nodes")
	}
	if got.Count != "1 of 4" {
		t.Errorf("count %q", got.Count)
	}
}

func TestATaskViewBoardHasALanePerGroup(t *testing.T) {
	type lane struct {
		Label string
		Focus []string
	}
	var got []lane
	nodeRun(t, mixedPrelude(t), nil, `
const p = viewPage(route({ g: "source", layout: "board" }), "");
const board = byClass(h("div", {}, ...p.body), "board")[0];
process.stdout.write(JSON.stringify(byClass(board, "lane").map((l) => ({ Label: text(byClass(l, "lane-head")[0].children[0]),
  Focus: all(l, (x) => x.tag === "li").map((x) => x.attrs["data-focus"]) }))));`, mixedInput(t, nil), &got)
	want := []lane{{"github", []string{"task:g-1", "task:g-2"}}, {"slack", []string{"task:s-1", "task:s-2"}}}
	if !slices.EqualFunc(got, want, func(a, b lane) bool { return a.Label == b.Label && slices.Equal(a.Focus, b.Focus) }) {
		t.Errorf("lanes %+v, want %+v", got, want)
	}
}

func TestTheViewGroupIsTheLensDefault(t *testing.T) {
	var got struct {
		Group, First, Picked string
		Keys                 []string
	}
	nodeRun(t, mixedPrelude(t), nil, `
tasksState.key = viewQueryKey("waiting", "", []);
const p = viewPage(route({ view: "waiting" }), "");
const bar = lensBar(p.lens, p.res);
const flat = viewPage(route({ view: "waiting", g: NO_GROUP }), "");
process.stdout.write(JSON.stringify({ Group: p.res.group, Keys: p.res.groups.map((g) => g.key), First: text(byFocus(bar, "lens:group").children[0]),
  Picked: flat.res.group }));`, mixedInput(t, nil), &got)
	if got.Group != "waits-on" || !slices.Equal(got.Keys, []string{"bia", "me"}) {
		t.Errorf("Waiting groups by %q into %v, want waits-on [bia me]", got.Group, got.Keys)
	}
	if got.First != "Waits on (person) (default)" {
		t.Errorf("the Group menu's first option %q", got.First)
	}
	if got.Picked != "" {
		t.Errorf("picking no group still groups by %q", got.Picked)
	}
}

func TestAHashGroupTheViewLacksFallsBackKeepingTheQuery(t *testing.T) {
	var got []map[string]string
	nodeRun(t, routesPrelude(t)+treeJS, nil, `
catalog.fields.push(...input.slack.map(fieldOf));
tasksState.catalog = input.cat;
const read = (hash) => { location.hash = hash; const r = readHash(); return { g: r.g, s: r.s, q: r.q, layout: r.layout }; };
const before = read("#view=needs-me&g=ci&q=where:general");
tasksState.key = viewQueryKey("needs-me", "", []);
tasksState.tasks = input.tasks;
process.stdout.write(JSON.stringify([before,
  read("#view=needs-me&g=opened&s=moved&q=where:general&layout=board"),
  read("#view=needs-me&g=ci&s=source-desc"),
  read("#view=mine&g=source")]));`, mixedInput(t, nil), &got)
	want := []map[string]string{
		{"g": "ci", "s": "", "q": "where:general", "layout": ""},
		{"g": "", "s": "", "q": "where:general", "layout": "board"},
		{"g": "ci", "s": "source-desc", "q": "", "layout": ""},
		{"g": "", "s": "", "q": "", "layout": ""},
	}
	for i := range want {
		if !maps.Equal(got[i], want[i]) {
			t.Errorf("hash %d reads %v, want %v", i, got[i], want[i])
		}
	}
}

// TestTheTaskListAndTheRailCountAgreeOnAQualifier keeps the list a task view shows on the tasks the daemon
// counts for its rail: the same query over the same specs, the daemon's through the Go lens.
func TestTheTaskListAndTheRailCountAgreeOnAQualifier(t *testing.T) {
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	views := servedViews(t)
	specs := taskSpecs(t, views)
	es := mixedTasks()
	queries := []string{"where:general", "where:acme", "source:github ci:failure", "-source:slack", "waits-on:@me deploy", "tag:lang", "-where:general -where:random",
		"state:doing settings", "conversation:thread", "kind:checks kind:review", "nothing:here"}
	var got [][]string
	nodeRun(t, mixedPrelude(t), nil, `
Date.now = () => input.now;
process.stdout.write(JSON.stringify(input.queries.map((q) => groupedRows(viewPage(route({ q }), "").res.groups).map((it) => it.task.id).sort())));`,
		mixedInput(t, map[string]any{"queries": queries, "now": now.UnixMilli()}), &got)
	for i, q := range queries {
		query := lens.Parse(q, specs)
		var want []string
		for _, e := range es {
			if query.Match(lens.Item{Facets: e.Facets, Texts: e.Texts()}, specs, now) {
				want = append(want, e.ID)
			}
		}
		slices.Sort(want)
		if !slices.Equal(got[i], want) && !(len(got[i]) == 0 && len(want) == 0) {
			t.Errorf("%q: the list shows %v, the daemon counts %v", q, got[i], want)
		}
	}
}
