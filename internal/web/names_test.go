package web

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/domain/card"
	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/unit"
)

var slackUnit = unit.Unit{Provider: "slack", Key: "T0001/C0001/1700000000.000100"}

func TestRowLinkNamesItsProvider(t *testing.T) {
	src := appJS(t)
	var b strings.Builder
	b.WriteString(treeJS)
	b.WriteString(keyJS(t))
	b.WriteString(jsFunction(t, src, "pl"))
	b.WriteString(pluginsJS(t))
	var got []string
	nodeRun(t, b.String(), []string{"where"}, `
applyPlugins(input.plugins);
process.stdout.write(JSON.stringify(input.rows.map((r) => {
  const a = where(r, "row:x").find((n) => n.tag === "a");
  return a.attrs.class + " " + text(a);
})));`, map[string]any{
		"plugins": []api.Plugin{{ID: unit.GitHub, Name: "GitHub", Enabled: true}, {ID: "slack", Name: "Slack", Enabled: true}},
		"rows": []api.Row{{Unit: slackUnit, Link: "https://app.slack.com/client/T0001/C0001"},
			{Unit: prUnit, Link: "https://github.com/acme/api/pull/8"}},
	}, &got)
	if len(got) != 2 || got[0] != "ext Slack" || got[1] != "ext GitHub" {
		t.Errorf("links %q, want Slack then GitHub", got)
	}
}

func slackTask() api.TaskView {
	at := time.Date(2026, 10, 23, 14, 15, 0, 0, time.UTC)
	return task("e-9", "waiting", func(e *api.TaskView) {
		e.Kind = "waiting"
		e.Units = []unit.Unit{slackUnit}
		e.UnitLabels = []string{"#general"}
		e.Items = []api.ItemView{{Unit: slackUnit, ID: "e1", Summary: "Ana owes the doc", State: "open", Kind: "request"}}
		e.Asks = []api.AskView{{Item: "e1", Summary: "Ana owes the doc", Asker: "Ana"}}
		e.Cards = []api.CardView{{Card: card.Card{Unit: slackUnit, Label: "#general", Title: "Deploy plan", State: "waits_on_others",
			Signals: []card.Signal{{Key: "last activity", Value: "2026-10-23 14:15 UTC", Tone: "info", At: at}},
			WaitsOn: []items.Person{{Key: "group:S0001", Name: "@team-a"}}}}}
	})
}

func TestSlackTaskDetailReadsByName(t *testing.T) {
	var got struct {
		Text, Head string
		Pills      int
	}
	nodeRun(t, viewsPrelude(t)+detailHarness, nil, `
globalThis.document = { activeElement: null, body: { classList: { add() {} } } };
(async () => {
  await loadDetailPane({ task: "e-9" });
  const box = { dataset: {}, contains: () => false, replaceChildren(...ns) { this.kids = ns.map((n) => Array.isArray(n) ? String(n) : n); } };
  renderTaskDetail(box, { task: "e-9" });
  const head = byClass(box.kids, "d-head")[0];
  process.stdout.write(JSON.stringify({ Text: text(box.kids), Head: text(head), Pills: byClass(head, "pill").filter((p) => text(p) === "waiting").length }));
})();`, map[string]any{"answers": map[string]any{"/api/tasks/e-9": slackTask()}}, &got)
	if strings.Contains(got.Text, "[object ") {
		t.Errorf("the detail prints an element list as text: %s", got.Text)
	}
	if got.Pills != 1 {
		t.Errorf("%d waiting pills in the head %q, want 1", got.Pills, got.Head)
	}
	for _, s := range []string{"#general", "Ana owes the doc", "@team-a", "last activity 2026-10-23T14:15:00Z"} {
		if !strings.Contains(got.Text, s) {
			t.Errorf("the detail lacks %q: %s", s, got.Text)
		}
	}
	for _, s := range []string{"C0001", "S0001", "UTC"} {
		if strings.Contains(got.Text, s) {
			t.Errorf("the detail shows %q: %s", s, got.Text)
		}
	}
}

func TestGroupChipFiltersToItsRow(t *testing.T) {
	row := map[string]any{
		"unit": map[string]string{"provider": "slack", "key": slackUnit.Key}, "label": "#general", "title": "Deploy plan", "kind": "waits_on_others",
		"views": []string{"slack", "all"}, "summary": map[string]any{"waits_on": "@team-a, Ana, channel participants"},
		"facets":  map[string][]string{"conversation": {"#general"}, "waits-on": {"@team-a", "Ana", "channel participants"}},
		"payload": map[string]any{"waits_on": []map[string]string{{"key": "group:S0001", "name": "@team-a"}, {"key": "slack:U0002", "name": "Ana"}, {"key": "group:channel participants", "name": "channel participants"}}},
	}
	other := map[string]any{"unit": map[string]string{"provider": "slack", "key": "T0001/C0002/1700000000.000200"}, "label": "#random", "kind": "waits_on_others",
		"views": []string{"slack", "all"}, "summary": map[string]any{"waits_on": "Bo"}, "facets": map[string][]string{"waits-on": {"Bo"}}}
	var got struct {
		Titles []string
		Shown  [][]string
	}
	nodeRun(t, lensPrelude(t)+treeJS, append(slices.Clone(lensFuncs), "fieldById", "foldMap", "marked", "facet", "whoFacets", "pl", "sm",
		"qualifierToken", "toggledQualifier", "withoutQualifiers"), `
const lens0 = { view: "all", q: "", ts: [], quals: [], filters: new Map(), group: "", sort: "", layout: "" };
const chips = whoFacets(input.rows[0], lens0, "row:x:").filter((n) => n && n.tag === "button");
const shown = chips.map((c) => {
  const q = toggledQualifier("", "waits-on", text(c), false);
  const lens = { view: "all", q, ts: terms(q), quals: qualifiers(q), filters: filtersOf(qualifiers(q)), group: "", sort: "", layout: "", done: false };
  return pipeline(input.rows, lens).groups.flatMap((g) => g.rows.map((r) => r.label));
});
process.stdout.write(JSON.stringify({ Titles: chips.map((c) => c.attrs["aria-label"]), Shown: shown }));`,
		map[string]any{"rows": []any{row, other}}, &got)
	want := []string{"Filter by waits on (group) @team-a", "Filter by waits on (person) Ana", "Filter by waits on (group) channel participants"}
	if !slices.Equal(got.Titles, want) {
		t.Errorf("chips %q, want %q", got.Titles, want)
	}
	for i, s := range got.Shown {
		if !slices.Equal(s, []string{"#general"}) {
			t.Errorf("chip %d keeps %v, want the row it came from", i, s)
		}
	}
}

func TestUnitColumnNamesTheViewsProvider(t *testing.T) {
	views := []api.ViewSpec{{ID: "needs-me", Provider: "github", Rows: true, Unit: "Pull request", UnitSort: "repo"},
		{ID: "all", Provider: "github", Rows: true}, {ID: "slack", Provider: "slack", Rows: true, Unit: "Conversation", UnitSort: "channel"}}
	var got []map[string]string
	nodeRun(t, `const catalog = { views: [] };
const fieldById = (id) => ({ repo: { label: "Repo" }, channel: { label: "Channel" } })[id];
`, []string{"unitColumn"}, `catalog.views = input.views;
process.stdout.write(JSON.stringify(["needs-me", "slack", "all"].map(unitColumn)));`, map[string]any{"views": views}, &got)
	want := []map[string]string{{"title": "Pull request", "sort": "repo", "sortTitle": "Sort by repo"},
		{"title": "Conversation", "sort": "channel", "sortTitle": "Sort by channel"}, {"title": "Item", "sort": "repo", "sortTitle": "Sort by repo"}}
	if len(got) != 3 {
		t.Fatalf("columns %v", got)
	}
	for i := range want {
		for k, v := range want[i] {
			if got[i][k] != v {
				t.Errorf("view %d %s = %q, want %q", i, k, got[i][k], v)
			}
		}
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
	if len(got) != 3 || got[0].Title != "" || !slices.Equal(got[0].Views, coreViewIDs) ||
		got[1].Title != "GitHub" || !slices.Equal(got[1].Views, []string{"needs-me", "all"}) ||
		got[2].Title != "Slack" || !slices.Equal(got[2].Views, []string{"slack"}) {
		t.Errorf("sections %+v", got)
	}
}
