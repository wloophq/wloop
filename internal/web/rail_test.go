package web

import (
	"slices"
	"testing"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/domain/unit"
)

// railDoc stands in for the page's rail: its views, people and omnibox entry, each keeping what it was given.
const railDoc = `
const railEls = {};
for (const id of ["views", "people", "omnibox-entry"]) railEls[id] = { id, kids: [], replaceChildren(...ns) { this.kids = ns; } };
globalThis.document = { getElementById: (id) => railEls[id], querySelector: () => null };
const railRoute = (view) => ({ view, area: "", ctx: [], q: "", g: "", s: "", layout: "", task: "" });
const railLinks = () => all(railEls.views.kids, (x) => x.tag === "a").map((a) => text(a.children[0]));
`

func TestTheRailCollapsesEmptyViews(t *testing.T) {
	var got struct {
		Closed, Open         []string
		Label, Count         string
		Expanded, ExpandedOn string
		Omnibox              string
	}
	counters := api.Counters{Views: map[string]int{"inbox": 3, "someday": 0, "logbook": 0}}
	nodeRun(t, viewsPrelude(t)+railDoc, nil, `
tasksState.catalog = input.cat;
globalThis.cockpitKeys = { bindings: () => [{ action: "palette", key: "Cmd+K" }] };
const nav = viewNav(input.cat, [], [], input.counters);
renderRail(nav, railRoute("someday"));
const folder = byFocus(railEls.views.kids, "rail:empties");
const closed = railLinks();
const expanded = folder.attrs["aria-expanded"];
folder.attrs.onclick();
process.stdout.write(JSON.stringify({ Closed: closed, Open: railLinks(), Label: text(folder.children[0]), Count: text(folder.children[1]),
  Expanded: expanded, ExpandedOn: byFocus(railEls.views.kids, "rail:empties").attrs["aria-expanded"], Omnibox: text(railEls["omnibox-entry"].kids) }));`,
		map[string]any{"cat": viewsCatalog(), "counters": counters}, &got)
	if want := []string{"Home", "Focus", "Inbox", "Outbox", "Fits now", "Waiting", "Projects", "Someday", "Plan", "Recent"}; !slices.Equal(got.Closed, want) {
		t.Errorf("rail %v, want %v: Logbook folds, Someday is the view shown, the plan not loaded yet and Recent, which counts nothing, stay", got.Closed, want)
	}
	if got.Label != "Empty" || got.Count != "1" || got.Expanded != "false" {
		t.Errorf("the Empty entry reads %q %q, expanded %q", got.Label, got.Count, got.Expanded)
	}
	if got.ExpandedOn != "true" || got.Open[len(got.Open)-1] != "Logbook" {
		t.Errorf("opened, the Empty entry is expanded %q and the rail %v", got.ExpandedOn, got.Open)
	}
	if got.Omnibox != "Search or ask⌘K" {
		t.Errorf("the omnibox entry reads %q", got.Omnibox)
	}
}

func TestRailListsHomeFirstAndHidesRailFalse(t *testing.T) {
	cat := viewsCatalog()
	cat.Views = append(cat.Views, api.TaskViewSpec{ID: "home-blocked", Label: "Blocked by you"})
	counters := api.Counters{Views: map[string]int{"inbox": 3, "focus": 1, "outbox": 1, "fits-now": 1, "waiting": 1, "projects": 1, "someday": 1, "logbook": 1}}
	var got struct {
		Links    []string
		Hrefs    []string
		HomeOn   string
		InboxOn  string
		HomeHref string
	}
	nodeRun(t, viewsPrelude(t)+railDoc, nil, `
tasksState.catalog = input.cat;
const nav = viewNav(input.cat, [], [], input.counters);
renderRail(nav, railRoute("home"));
const links = all(railEls.views.kids, (x) => x.tag === "a");
const homeOn = links[0].attrs["aria-current"];
renderRail(nav, railRoute("inbox"));
const again = all(railEls.views.kids, (x) => x.tag === "a");
process.stdout.write(JSON.stringify({ Links: railLinks(), Hrefs: again.map((a) => a.attrs.href), HomeOn: homeOn || "", InboxOn: again[0].attrs["aria-current"] || "", HomeHref: again[0].attrs.href }));`,
		map[string]any{"cat": cat, "counters": counters}, &got)
	if len(got.Links) == 0 || got.Links[0] != "Home" || got.HomeHref != "#view=home" || slices.Contains(got.Links, "Blocked by you") {
		t.Fatalf("rail %v, home href %q", got.Links, got.HomeHref)
	}
	if got.HomeOn != "page" || got.InboxOn != "" {
		t.Errorf("home current on the home %q, on the inbox %q", got.HomeOn, got.InboxOn)
	}
}

func TestTheRailLeavesOutViewsOffTheRail(t *testing.T) {
	cat := viewsCatalog()
	for i := range cat.Views {
		cat.Views[i].Rail = cat.Views[i].ID != "someday" && cat.Views[i].ID != "logbook"
	}
	counters := api.Counters{Views: map[string]int{"inbox": 3, "someday": 2, "logbook": 0}}
	var got struct{ Inbox, Opened []string }
	nodeRun(t, viewsPrelude(t)+railDoc, nil, `
tasksState.catalog = input.cat;
railState.emptiesOpen = true;
const nav = viewNav(input.cat, [], [], input.counters);
renderRail(nav, railRoute("inbox"));
const inbox = railLinks();
renderRail(nav, railRoute("someday"));
process.stdout.write(JSON.stringify({ Inbox: inbox, Opened: railLinks() }));`,
		map[string]any{"cat": cat, "counters": counters}, &got)
	for _, off := range []string{"Someday", "Logbook"} {
		if slices.Contains(got.Inbox, off) {
			t.Errorf("rail %v lists %s, which is off the rail, as an entry or under Empty", got.Inbox, off)
		}
	}
	if !slices.Contains(got.Opened, "Someday") || slices.Contains(got.Opened, "Logbook") {
		t.Errorf("rail %v: the view shown stays reachable even off the rail, the others stay out", got.Opened)
	}
}

func TestTheRailHeadsEachPluginSection(t *testing.T) {
	served := []api.ViewSpec{{ID: "needs-me", Label: "Needs me", Provider: string(unit.GitHub)}, {ID: "slack", Label: "Conversations", Provider: "slack"},
		{ID: "all", Label: "All", Provider: string(unit.GitHub)}}
	var got struct {
		Heads, Links []string
	}
	nodeRun(t, viewsPrelude(t)+railDoc, nil, `
applyPlugins(input.plugins);
tasksState.catalog = input.cat;
renderRail(viewNav(input.cat, input.served, [], null), railRoute("inbox"));
process.stdout.write(JSON.stringify({ Heads: byClass(railEls.views.kids, "rail-head").map(text), Links: railLinks() }));`,
		map[string]any{"cat": viewsCatalog(), "served": served,
			"plugins": []api.Plugin{{ID: unit.GitHub, Name: "GitHub", Enabled: true}, {ID: "slack", Name: "Slack", Enabled: true}}}, &got)
	if want := []string{"GitHub", "Slack"}; !slices.Equal(got.Heads, want) {
		t.Errorf("section heads %v, want %v", got.Heads, want)
	}
	if want := []string{"Needs me", "All", "Conversations"}; !slices.Equal(got.Links[len(got.Links)-3:], want) {
		t.Errorf("rail %v ends with %v", got.Links, want)
	}
}

func TestNavListsCoreThenPluginViews(t *testing.T) {
	var got struct {
		IDs    []string `json:"ids"`
		Core   []bool   `json:"core"`
		Counts []any    `json:"counts"`
	}
	counters := api.Counters{Views: map[string]int{"focus": 1, "inbox": 4, "outbox": 2, "fits-now": 7, "waiting": 3, "projects": 0, "someday": 5, "logbook": 9}}
	nodeRun(t, viewsPrelude(t), nil, `
const nav = viewNav(input.cat, catalog.views, ["fake-board"], input.counters);
process.stdout.write(JSON.stringify({ ids: nav.map((v) => v.id), core: nav.map((v) => v.core), counts: nav.filter((v) => v.core).map((v) => v.count) }));`,
		map[string]any{"cat": viewsCatalog(), "counters": counters}, &got)
	plugin := []string{"mine", "dash", "all", "explore"}
	want := slices.Concat(coreViewIDs, coreRoutes, plugin, []string{"fake-board"})
	if !slices.Equal(got.IDs, want) {
		t.Fatalf("nav %v, want %v", got.IDs, want)
	}
	if got.IDs[0] != "focus" {
		t.Errorf("first view %q, want focus", got.IDs[0])
	}
	for i, c := range got.Core {
		if c != (i < len(coreViewIDs)+len(coreRoutes)) {
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
	nodeRun(t, viewsPrelude(t), nil, `
const served = catalog.views;
const nav = viewNav(input.cat, [], [], null);
process.stdout.write(JSON.stringify({
  ids: nav.map((v) => v.id),
  view: resolveView("mine", input.cat, [], () => false),
  before: resolveView("mine", null, [], () => false),
  enabled: resolveView("mine", input.cat, served, () => false),
  core_stay: resolveView("waiting", input.cat, [], () => false),
}));`, map[string]any{"cat": viewsCatalog()}, &got)
	if !slices.Equal(got.IDs, slices.Concat(coreViewIDs, coreRoutes)) {
		t.Errorf("nav with the GitHub plugin off %v, want the core views, the plan and Recent", got.IDs)
	}
	if got.View != "focus" {
		t.Errorf("#view=mine with the plugin off shows %q, want focus", got.View)
	}
	if got.Before != "mine" || got.Enabled != "mine" || got.CoreStay != "waiting" {
		t.Errorf("before load %q, plugin on %q, core %q", got.Before, got.Enabled, got.CoreStay)
	}
}

func TestRailGroupsViewsByPlugin(t *testing.T) {
	served := []api.ViewSpec{{ID: "needs-me", Label: "Needs me", Provider: "github"}, {ID: "slack", Label: "Slack", Provider: "slack"},
		{ID: "all", Label: "All", Provider: "github"}}
	var got []struct {
		Title string
		Views []string
	}
	nodeRun(t, viewsPrelude(t), nil, `
const nav = viewNav(input.cat, input.served, [], null);
const names = { github: "GitHub", slack: "Slack" };
process.stdout.write(JSON.stringify(railSections(nav, (p) => names[p]).map((s) => ({ Title: s.title, Views: s.views.map((v) => v.id) }))));`,
		map[string]any{"cat": viewsCatalog(), "served": served}, &got)
	if len(got) != 3 || got[0].Title != "" || !slices.Equal(got[0].Views, slices.Concat(coreViewIDs, coreRoutes)) ||
		got[1].Title != "GitHub" || !slices.Equal(got[1].Views, []string{"needs-me", "all"}) ||
		got[2].Title != "Slack" || !slices.Equal(got[2].Views, []string{"slack"}) {
		t.Errorf("sections %+v", got)
	}
}

func TestThePlanIsACoreViewWithItsCountWhateverPluginRuns(t *testing.T) {
	var got struct {
		Core    bool   `json:"core"`
		Label   string `json:"label"`
		View    string `json:"view"`
		Count   any    `json:"count"`
		Section string `json:"section"`
	}
	app := appJS(t)
	nodeRun(t, viewsPrelude(t)+jsConst(t, app, "PLAN_ORDER")+jsFunction(t, app, "planGroups")+jsFunction(t, app, "planCount"), nil, `
const nav = viewNav(input.cat, [], [], null);
const plan = nav.find((v) => v.id === "plan");
state.plan = { items: [{ decision: "pending" }, { decision: "blocked" }, { decision: "run" }], loaded: true };
const secs = railSections(nav, (p) => p);
process.stdout.write(JSON.stringify({ core: plan.core, label: plan.label, view: resolveView("plan", input.cat, [], () => false),
  count: railCount(plan, {}), section: secs.find((s) => s.views.some((v) => v.id === "plan")).title }));`,
		map[string]any{"cat": viewsCatalog()}, &got)
	if !got.Core || got.Label != "Plan" || got.View != "plan" || got.Section != "" || got.Count != float64(2) {
		t.Fatalf("%+v: the plan sits among the core views, counted, with no plugin running", got)
	}
}

func TestRecentIsACoreViewWhateverPluginRuns(t *testing.T) {
	var got struct {
		Core    bool   `json:"core"`
		Label   string `json:"label"`
		View    string `json:"view"`
		Count   any    `json:"count"`
		Section string `json:"section"`
		Served  bool   `json:"served"`
	}
	nodeRun(t, viewsPrelude(t), nil, `
const nav = viewNav(input.cat, [], [], null);
const recent = nav.find((v) => v.id === "recent");
const secs = railSections(nav, (p) => p);
process.stdout.write(JSON.stringify({ core: recent.core, label: recent.label, view: resolveView("recent", input.cat, [], () => false),
  count: railCount(recent, {}), section: secs.find((s) => s.views.some((v) => v.id === "recent")).title,
  served: catalog.views.some((v) => v.id === "recent") }));`,
		map[string]any{"cat": viewsCatalog()}, &got)
	if !got.Core || got.Label != "Recent" || got.View != "recent" || got.Section != "" || got.Count != "" || got.Served {
		t.Fatalf("%+v: Recent sits among the core views, uncounted, with no plugin running and no plugin serving it", got)
	}
}

func TestTheOmniboxEntriesShowTheServedPaletteKey(t *testing.T) {
	var got struct{ Bound, Unbound []string }
	nodeRun(t, viewsPrelude(t), nil, `
const box = { kids: [], replaceChildren(...n) { this.kids = n; } };
const kbds = () => all(box.kids, (x) => x.tag === "kbd").map(text);
railOmnibox(box);
const unbound = kbds();
globalThis.cockpitKeys = { bindings: () => [{ action: "palette", key: "Ctrl+P" }] };
railOmnibox(box);
process.stdout.write(JSON.stringify({ Bound: kbds(), Unbound: unbound }));`, nil, &got)
	if !slices.Equal(got.Bound, []string{"⌃P"}) || len(got.Unbound) != 0 {
		t.Fatalf("%+v: the rail shows the palette's served key, and none when nothing is bound", got)
	}
}
