package web

import (
	"encoding/json"
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
	nodeRun(t, b.String(), []string{"where", "rowLink"}, `
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
		e.Units = []unit.Unit{slackUnit}
		e.UnitLabels = []string{"#general"}
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

func TestEveryWhoChipFiltersToItsRow(t *testing.T) {
	row := map[string]any{
		"unit": map[string]string{"provider": "slack", "key": slackUnit.Key}, "label": "#general", "title": "Deploy plan", "kind": "waits_on_others",
		"views": []string{"slack", "all"}, "summary": map[string]any{"waits_on": "@team-a, Ana, channel participants"},
		"facets":  map[string][]string{"conversation": {"#general"}, "waits-on": {"@team-a", "Ana", "channel participants"}},
		"payload": map[string]any{"waits_on": []map[string]string{{"key": "group:S0001", "name": "@Team-A"}, {"key": "slack:U0002", "name": "Ana"}, {"key": "group:channel participants", "name": "channel participants"}}},
	}
	other := map[string]any{"unit": map[string]string{"provider": "slack", "key": "T0001/C0002/1700000000.000200"}, "label": "#random", "kind": "waits_on_others",
		"views": []string{"slack", "all"}, "summary": map[string]any{"waits_on": "Bo"}, "facets": map[string][]string{"waits-on": {"Bo"}}}
	var got struct {
		Titles []string
		Shown  [][]string
	}
	nodeRun(t, cockpitScript(t), nil, `
const c = input.catalog;
c.facets.push({ id: "waits-on", label: "Waits on (person)", provider: "slack", group: true, match: "team", menu: true, show: "who" });
applyCatalog(c);
tasksState.catalog = input.views;
const lens0 = lensOf({ ...readHash(), view: "all" });
const m = rowModel(input.rows[0], "", lens0, "");
const chips = pieceButtons(m, lens0, "who");
const shown = chips.map((b) => {
  const q = toggledQualifier("", "waits-on", b.textContent, false);
  const lens = { view: "all", q, ts: terms(q), quals: qualifiers(q), filters: filtersOf(qualifiers(q)), group: "", sort: "", layout: "", done: false };
  return pipeline(input.rows, lens).groups.flatMap((g) => g.rows.map((r) => r.label));
});
process.stdout.write(JSON.stringify({ Titles: chips.map((b) => b.attrs["aria-label"]), Shown: shown }));`,
		map[string]any{"rows": []any{row, other}, "catalog": json.RawMessage(servedCatalog(t)), "views": servedViews(t)}, &got)
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

func TestUnitColumnNamesTheViewsUnit(t *testing.T) {
	views := []api.ViewSpec{{ID: "needs-me", Provider: "github", Rows: true, Unit: "Pull request"},
		{ID: "all", Provider: "github", Rows: true}, {ID: "slack", Provider: "slack", Rows: true, Unit: "Conversation"}}
	var got []string
	nodeRun(t, `const catalog = { views: [] };
`, []string{"unitTitle"}, `catalog.views = input.views;
process.stdout.write(JSON.stringify(["needs-me", "slack", "all"].map((v) => unitTitle(v, { words: { col_unit: "Item" } }))));`, map[string]any{"views": views}, &got)
	if want := []string{"Pull request", "Conversation", "Item"}; !slices.Equal(got, want) {
		t.Errorf("columns %q, want %q", got, want)
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
