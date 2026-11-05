package web

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/brand/brandtest"
	"github.com/wloophq/wloop/internal/domain/unit"
	psource "github.com/wloophq/wloop/internal/plugins/source"
	"github.com/wloophq/wloop/internal/readmodel"
)

type bundledProvider = readmodel.Provider

type bundledPresenter struct {
	bundledProvider
	psource.Catalog
}

// bundledCatalog is the catalog the bundled GitHub and Slack plugins serve together, their facets merged as
// the daemon merges them.
func bundledCatalog(t *testing.T) json.RawMessage {
	t.Helper()
	c := readmodel.BuildCatalog(bundledPresenter{Catalog: psource.BundledCatalog(string(unit.GitHub))}, bundledPresenter{Catalog: psource.BundledCatalog("slack")})
	raw, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	return json.RawMessage(brandtest.Expand(string(raw)))
}

// stripJS reads back a rendered row's strip: the field and value of its dot's facet button, its strip classes
// and the field its who column's head sorts by.
const stripJS = `
function dotOf(n) {
  if (n && n.attrs && n.attrs.class === "dot") return n;
  for (const c of (n && n.children) || []) { const d = dotOf(c); if (d) return d; }
  return null;
}
function stripShown(m, tr, lens, cols, it) {
  const dot = dotOf(tr);
  const walk = (n) => n && n.tag === "button" && n.attrs.class === "facet" && dotOf(n) ? n : ((n && n.children) || []).map(walk).find(Boolean);
  const button = walk(tr);
  const span = dot ? dot.attrs : {};
  const quals = button ? qualifiers(toggledQualifier("", span["data-field"], span["data-value"], false, lens.fields), lens.fields) : [];
  const who = rowHead(cols, lens, "").children[0].children.find((th) => th.attrs.class === "c-wait");
  const sort = who && who.children[0] && who.children[0].attrs ? who.children[0].attrs["data-sort"] || "" : "";
  return { Field: span["data-field"] || "", Value: span["data-value"] || "", Button: !!button, Kept: quals.length === 1 && passesFilters(it, filtersOf(quals, lens.fields)),
    Strip: tr.attrs.class.split(" ").filter((c) => c.startsWith("w-")), Sort: sort, Tip: tr.attrs.title || "" };
}
function rowStripShown(r, view) {
  const lens = lensOf({ ...readHash(), view });
  const m = rowModel(r, "", lens, "");
  const cols = rowColumns([m]);
  return stripShown(m, rowTr(m, lens, cols), lens, cols, r);
}
function taskStripShown(e, view) {
  const lens = lensOf({ ...readHash(), view });
  const m = taskModel(e, "", "", lens);
  const cols = rowColumns([m]);
  return stripShown(m, rowTr(m, lens, cols), lens, cols, taskItemOf(e));
}
`

type stripRow struct {
	Field, Value, Sort, Tip string
	Button, Kept            bool
	Strip                   []string
}

// TestEachRowsStripIsItsOwnProviders renders a pull request, a Slack conversation, a row of a provider with no
// strip and a task: each row's dot filters its own provider's strip facet and keeps the row, its strip class
// is its value's hue, a row with no strip has neither, and the who column's head sorts by the view's strip.
func TestEachRowsStripIsItsOwnProviders(t *testing.T) {
	now := time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC)
	pr := tagRows()[1]
	pr["views"] = []string{"mine"}
	slack := map[string]any{"unit": map[string]any{"provider": "slack", "key": slackUnit.Key}, "label": "#general", "title": "Deploy plan", "kind": "waits_on_others",
		"views": []string{"slack"}, "summary": map[string]any{"waits_on": "Ana", "since": "2026-10-12T09:00:00Z"},
		"facets": map[string][]string{"where": {"general"}, "waits-on": {"Ana"}, "turn": {"others"}, "situation": {"waits_on_others"}, "age": {"2026-10-12T09:00:00Z"}}}
	other := map[string]any{"unit": map[string]any{"provider": "thread", "key": "C1/171.1"}, "label": "C1", "title": "A thread", "kind": "new",
		"views": []string{"slack"}, "summary": map[string]any{"waits_on": "Ana"}, "facets": map[string][]string{"waits-on": {"Ana"}, "turn": {"others"}}}
	e := task("e-1", "next", func(e *api.TaskView) {
		e.Facets = map[string][]string{"state": {"next"}, "waits-on": {"me"}}
	})
	var got struct {
		PR, Slack, Other, Task stripRow
		SlackPieces            []string
		Head                   string
		Legend                 string
	}
	rowViewRun(t, bundledCatalog(t), stripJS+`state.rows = [input.pr, input.slack, input.other];
const slackLens = lensOf({ ...readHash(), view: "slack" });
const slackModel = rowModel(input.slack, "", slackLens, "");
const head = rowTable({ group: "turn", groups: [{ field: "turn", key: "others", raw: "others", label: "Others' turn", rows: [input.slack] }] },
  (r) => rowModel(r, "", slackLens, ""), slackLens, rowColumns([slackModel]))[0].children[0];
process.stdout.write(JSON.stringify({ PR: rowStripShown(input.pr, "mine"), Slack: rowStripShown(input.slack, "slack"), Other: rowStripShown(input.other, "slack"),
  Task: taskStripShown(input.task, "inbox"), SlackPieces: Object.values(slackModel.parts).flat().map((p) => p.field + ":" + p.label), Head: head.attrs.class,
  Legend: JSON.stringify(stripLegend(slackLens, slackLens.fields)) }));`,
		map[string]any{"pr": wireRow("me", now, pr), "slack": slack, "other": other, "task": e, "now": now.UnixMilli()}, &got)
	if got.PR.Field != "wait" || !got.PR.Button || !got.PR.Kept || len(got.PR.Strip) != 1 || got.PR.Strip[0] != "w-"+got.PR.Value || got.PR.Sort != "wait" {
		t.Errorf("the pull request's strip %+v, want GitHub's wait, its hue, and the who head sorting by wait", got.PR)
	}
	if got.Slack.Field != "turn" || got.Slack.Value != "others" || !got.Slack.Kept || !slices.Equal(got.Slack.Strip, []string{"w-author"}) || got.Slack.Sort != "turn" {
		t.Errorf("the conversation's strip %+v, want Slack's turn, others in the author hue, and the who head sorting by turn", got.Slack)
	}
	if !strings.HasPrefix(got.Slack.Tip, "Others' turn (Ana): waits on others") {
		t.Errorf("the conversation's tooltip %q", got.Slack.Tip)
	}
	for _, want := range []string{"situation:waits on others", "age:3h 0m"} {
		if !slices.Contains(got.SlackPieces, want) {
			t.Errorf("the conversation lacks %s: %v", want, got.SlackPieces)
		}
	}
	if got.Other.Field != "" || len(got.Other.Strip) != 0 {
		t.Errorf("a row whose provider has no strip shows one: %+v", got.Other)
	}
	if got.Task.Field != "waits-on" || got.Task.Value != "me" || !got.Task.Kept || !slices.Equal(got.Task.Strip, []string{"w-me"}) {
		t.Errorf("the task's strip %+v, want the profile's waits-on, me in its hue", got.Task)
	}
	if !strings.Contains(" "+got.Head+" ", " w-author ") {
		t.Errorf("the head of the others group is %q, want the author hue", got.Head)
	}
	for _, want := range []string{`"lg w-me"`, `"Your turn"`, `"lg w-author"`, `"Others' turn"`, `"lg w-nobody"`} {
		if !strings.Contains(got.Legend, want) {
			t.Errorf("the Slack legend lacks %s: %s", want, got.Legend)
		}
	}
}

// TestTheBacklogKeepsGitHubsStrip charts the backlog's wait panel over a catalog whose first strip facet is
// another plugin's.
func TestTheBacklogKeepsGitHubsStrip(t *testing.T) {
	now := time.Now()
	var got struct {
		Bars   map[string]int
		Colors []string
	}
	dashRun(t, "", dashRows(now), now, `
const c = `+servedCatalog(t)+`;
c.facets.unshift({ id: "turn", label: "Whose turn", provider: "slack", group: true, sort: "rank", rank: ["me", "others", "nobody"], menu: true, strip: true,
  colors: { me: "me", others: "author", nobody: "nobody" } });
applyCatalog(c);
const o = dashWaitOption(rows, lens, pal);
process.stdout.write(JSON.stringify({ Bars: bars(o), Colors: o.series[0].data.map((d) => d.itemStyle.color) }));`, &got)
	if got.Bars["Waiting on me"] == 0 {
		t.Errorf("the wait panel %v, want GitHub's wait", got.Bars)
	}
	for _, c := range got.Colors {
		if !slices.Contains([]string{testPal["me"], testPal["author"], testPal["reviewer"], testPal["loop"], testPal["nobody"]}, c) {
			t.Errorf("a bar's color %q is no strip hue: %v", c, got.Colors)
		}
	}
}
