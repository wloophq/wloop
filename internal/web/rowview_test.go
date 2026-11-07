package web

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/domain/unit"
)

type shownPiece struct {
	Field, Value, Label string
	Kept                bool
}

type shownRow struct {
	Pieces  []shownPiece
	Classes []string
	Heads   []string
	Text    string
}

// rowViewJS renders a row or a task with the row renderer and reads back each facet button: the qualifier
// its click adds, whether the item still passes that filter, and the row's classes and column heads.
const rowViewJS = `
function shown(tr, it, lens) {
  const out = [];
  const walk = (n) => {
    if (n && n.tag === "button" && n.attrs.class === "facet") {
      const span = n.children[0];
      const f = span.attrs["data-field"], v = span.attrs["data-value"];
      const quals = qualifiers(toggledQualifier("", f, v, false, lens.fields), lens.fields);
      out.push({ Field: f, Value: v, Label: span.textContent, Kept: quals.length === 1 && passesFilters(it, filtersOf(quals, lens.fields)) });
    }
    for (const c of (n && n.children) || []) walk(c);
  };
  walk(tr);
  return out;
}
function heads(head) {
  return head.children[0].children.map((th) => th.textContent);
}
function rowShown(r, view) {
  const lens = lensOf({ ...readHash(), view });
  const m = rowModel(r, "", lens, "");
  const cols = rowColumns([m]);
  const tr = rowTr(m, lens, cols);
  return { Pieces: shown(tr, r, lens), Classes: tr.attrs.class.split(" "), Heads: heads(rowHead(cols, lens, unitTitle(view, lens))), Text: tr.textContent };
}
function taskShown(e, view) {
  const lens = lensOf({ ...readHash(), view });
  const m = taskModel(e, "", "", lens);
  const cols = rowColumns([m]);
  const tr = rowTr(m, lens, cols);
  return { Pieces: shown(tr, taskItemOf(e), lens), Classes: tr.attrs.class.split(" "), Heads: heads(rowHead(cols, lens, "")), Text: tr.textContent };
}
`

func rowViewRun(t *testing.T, catalog json.RawMessage, main string, in map[string]any, out any) {
	t.Helper()
	in["catalog"], in["views"] = catalog, servedViews(t)
	nodeRun(t, cockpitScript(t)+rowViewJS, nil, `
Date.now = () => input.now;
applyCatalog(input.catalog);
tasksState.catalog = input.views;
state.health = { viewer: "me" };
state.specs = { version: "", byId: {} };
state.busy = new Set();
`+main, in, out)
}

func labelsOf(r shownRow) []string {
	var out []string
	for _, p := range r.Pieces {
		out = append(out, p.Field+":"+p.Label)
	}
	return out
}

// TestEveryPieceOfARowIsAFacetThatKeepsItsRow renders a pull request, a Slack conversation and a task with
// the one row renderer: every piece they show is a facet button whose qualifier keeps the item it came from,
// and each shows the pieces its provider's or the profile's specs place.
func TestEveryPieceOfARowIsAFacetThatKeepsItsRow(t *testing.T) {
	now := time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC)
	pr := tagRows()[1]
	pr["override"] = map[string]any{"hold": true}
	pr["reason"] = "changes requested by bia"
	pr["views"] = []string{"mine", "stalled"}
	slack := map[string]any{"unit": map[string]any{"provider": "slack", "key": slackUnit.Key}, "label": "#general", "title": "Deploy plan", "kind": "waits_on_others",
		"views": []string{"slack"}, "summary": map[string]any{"waits_on": "Ana", "since": "2026-10-12T09:00:00Z"},
		"facets": map[string][]string{"where": {"general"}, "waits-on": {"Ana"}, "conversation": {"#general"}}}
	e := task("e-1", "next", func(e *api.TaskView) {
		e.Title, e.Units, e.UnitLabels = "Answer the deploy question", []unit.Unit{slackUnit}, []string{"#general thread"}
		e.Facets = map[string][]string{"source": {"slack"}, "state": {"next"}, "waits-on": {"bia"}, "contexts": {"calls"}, "moved": {"2026-10-12T10:30:00Z"},
			"where": {"general"}, "risk": {"none"}}
	})
	var cat map[string]any
	if err := json.Unmarshal([]byte(servedCatalog(t)), &cat); err != nil {
		t.Fatal(err)
	}
	cat["facets"] = append(cat["facets"].([]any),
		map[string]any{"id": "where", "label": "Where", "provider": "slack", "group": true, "sort": "text", "match": "suffix", "menu": true, "show": "ref", "label_prefix": "#"},
		map[string]any{"id": "waits-on", "label": "Waits on (person)", "provider": "slack", "group": true, "match": "team", "menu": true, "show": "who"})
	cat["views"] = append(cat["views"].([]any), map[string]any{"id": "slack", "label": "Slack", "provider": "slack", "rows": true, "surfaces": []string{"web"}, "unit": "Conversation"})
	raw, err := json.Marshal(cat)
	if err != nil {
		t.Fatal(err)
	}
	var got struct{ PR, Slack, Task shownRow }
	rowViewRun(t, raw, `state.rows = [input.pr, input.slack];
process.stdout.write(JSON.stringify({ PR: rowShown(input.pr, "mine"), Slack: rowShown(input.slack, "slack"), Task: taskShown(input.task, "inbox") }));`,
		map[string]any{"pr": wireRow("me", now, pr), "slack": slack, "task": e, "now": now.UnixMilli()}, &got)
	for name, r := range map[string]shownRow{"pull request": got.PR, "slack": got.Slack, "task": got.Task} {
		if len(r.Pieces) == 0 {
			t.Errorf("the %s row shows no piece: %s", name, r.Text)
		}
		for _, p := range r.Pieces {
			if !p.Kept {
				t.Errorf("the %s row's %s:%s piece filters it out", name, p.Field, p.Value)
			}
			if p.Value == "none" || p.Label == "none" {
				t.Errorf("the %s row shows the none sentinel of %s", name, p.Field)
			}
		}
	}
	for _, want := range []string{"where:acme/web", "author:by me", "size:L", "risk:risk high", "tag:lang:go", "tag:lang:sql", "is:paused", "kind:changes asked"} {
		if !slices.Contains(labelsOf(got.PR), want) {
			t.Errorf("the pull request lacks %s: %v", want, labelsOf(got.PR))
		}
	}
	if !slices.Contains(got.PR.Classes, "mine") || !slices.Contains(got.PR.Classes, "late") {
		t.Errorf("the pull request's classes %v, want mine and late from its author and is facets", got.PR.Classes)
	}
	if want := []string{"Pull request", "Waiting on", "For", "Why", "Workspace"}; !slices.Equal(got.PR.Heads, want) {
		t.Errorf("the pull request's heads %q, want %q: a row view without actions draws no Actions column", got.PR.Heads, want)
	}
	if want := []string{"where:#general", "waits-on:Ana"}; !slices.Equal(labelsOf(got.Slack), want) {
		t.Errorf("the conversation shows %v, want %v", labelsOf(got.Slack), want)
	}
	if strings.Count(got.Slack.Text, "general") != 1 {
		t.Errorf("the conversation repeats its channel: %s", got.Slack.Text)
	}
	for _, want := range []string{"source:Slack", "contexts:@calls", "waits-on:bia", "state:Next", "moved:1h 30m"} {
		if !slices.Contains(labelsOf(got.Task), want) {
			t.Errorf("the task lacks %s: %v", want, labelsOf(got.Task))
		}
	}
	for _, p := range got.Task.Pieces {
		if p.Field == "where" {
			t.Errorf("the task shows a plugin's facet its profile places nowhere: %v", labelsOf(got.Task))
		}
	}
	if want := []string{"Item", "Waiting on", "For"}; !slices.Equal(got.Task.Heads, want) {
		t.Errorf("the task's heads %q, want %q: no reason, aside or actions draws no column", got.Task.Heads, want)
	}
}

// TestATaskTableDrawsAColumnSomeTaskFills checks that a task list's columns are those some task of the view
// has a piece for, before the filter, and that a group's head spans them.
func TestATaskTableDrawsAColumnSomeTaskFills(t *testing.T) {
	var got struct {
		Heads   [][]string
		Colspan []any
	}
	nodeRun(t, mixedPrelude(t), nil, `
const page = (q) => viewPage(route({ q, g: "source" }), "");
const table = (p) => all(h("div", {}, ...p.body), (x) => x.tag === "table")[0];
const heads = (tb) => all(tb, (x) => x.tag === "thead")[0].children[0].children.map(text);
const spans = (tb) => all(tb, (x) => x.tag === "th" && x.attrs.scope === "rowgroup").map((x) => x.attrs.colspan);
tasksState.tasks = input.tasks.map((e) => e.id === "g-1" ? { ...e, reason: "checks failing" } : e);
const all1 = table(page("")), narrow = table(page("where:general"));
process.stdout.write(JSON.stringify({ Heads: [heads(all1), heads(narrow)], Colspan: [...spans(all1), ...spans(narrow)] }));`, mixedInput(t, nil), &got)
	if len(got.Heads) != 2 || !slices.Contains(got.Heads[0], "Why") || !slices.Equal(got.Heads[0], got.Heads[1]) {
		t.Errorf("heads %q: the filter must not drop the Why column a filtered-out task fills", got.Heads)
	}
	for _, c := range got.Colspan {
		if c != float64(len(got.Heads[0])) {
			t.Errorf("a group head spans %v of %d columns", c, len(got.Heads[0]))
		}
	}
}

// TestARowViewRendersWithNoGitHubFacets renders a Slack row over a catalog that serves no GitHub facet, as
// with the GitHub plugin disabled.
func TestARowViewRendersWithNoGitHubFacets(t *testing.T) {
	cat := api.CatalogResponse{
		Views: []api.ViewSpec{{ID: "slack", Label: "Slack", Provider: "slack", Rows: true, Surfaces: []string{"web"}, Unit: "Conversation"}},
		Facets: []api.FacetSpec{{ID: "where", Label: "Where", Provider: "slack", Group: true, Match: "suffix", Menu: true, Show: "ref"},
			{ID: "waits-on", Label: "Waits on (person)", Provider: "slack", Group: true, Match: "team", Menu: true, Show: "who"}},
		Situations: []api.SituationSpec{{Provider: "slack", Kind: "new", Label: "new"}},
	}
	raw, err := json.Marshal(cat)
	if err != nil {
		t.Fatal(err)
	}
	slack := map[string]any{"unit": map[string]any{"provider": "slack", "key": slackUnit.Key}, "label": "#general", "title": "Deploy plan", "kind": "new",
		"views": []string{"slack"}, "summary": map[string]any{"waits_on": "Ana"}, "facets": map[string][]string{"where": {"general"}, "waits-on": {"Ana"}}, "actions": []any{}}
	var got shownRow
	rowViewRun(t, raw, `state.rows = [input.slack];
process.stdout.write(JSON.stringify(rowShown(input.slack, "slack")));`, map[string]any{"slack": slack, "now": 0}, &got)
	if want := []string{"where:general", "waits-on:Ana"}; !slices.Equal(labelsOf(got), want) {
		t.Errorf("pieces %v, want %v", labelsOf(got), want)
	}
	if got.Heads[0] != "Conversation" {
		t.Errorf("heads %q", got.Heads)
	}
}

// TestARowShowsWhatItsOwnPluginSays renders a mixed view's rows: the open one is selected, a row with
// actions draws the Actions column, its age tells the exact time, each row takes its own plugin's words, and a
// provider's row_class never marks another provider's row.
func TestARowShowsWhatItsOwnPluginSays(t *testing.T) {
	now := time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC)
	pr := tagRows()[0]
	pr["views"] = []string{"all"}
	pr["actions"] = []map[string]any{{"id": "open", "enabled": true}}
	slack := map[string]any{"unit": map[string]any{"provider": "slack", "key": slackUnit.Key}, "label": "#general", "title": "Deploy plan", "kind": "new",
		"views": []string{"all"}, "summary": map[string]any{"waits_on": "Ana"}, "facets": map[string][]string{"author": {"me"}, "waits-on": {"Ana"}}}
	var cat map[string]any
	if err := json.Unmarshal([]byte(servedCatalog(t)), &cat); err != nil {
		t.Fatal(err)
	}
	cat["lens"] = []any{map[string]any{"provider": "github", "words": map[string]string{"filter_label": "GitHub filter {{.value}}", "col_aside": "Workspace", "countdown_title": "GitHub sends at {{.time}}"}}}
	raw, err := json.Marshal(cat)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Class, Selected, AgeTitle string
		Heads                     []string
		Labels                    [][]string
		SlackClass                []string
		Count, Countdown          string
	}
	rowViewRun(t, raw, `state.rows = [input.pr, input.slack];
state.roundGates.set(keyOf(input.pr), { deadline: new Date(input.now + 600000).toISOString() });
const countdownTitle = countdown(input.pr).attrs.title;
state.roundGates.clear();
state.specs = { version: "", byId: { open: { id: "open", label: "Open", group: "control", surfaces: ["row"] } } };
const lens = lensOf({ ...readHash(), view: "all" });
const key = keyOf(input.pr);
const models = state.rows.map((r) => rowModel(r, key, lens, ""));
const cols = rowColumns(models);
const trs = models.map((m) => rowTr(m, lens, cols));
const labels = (n, out = []) => { if (n && n.tag === "button" && n.attrs.class === "facet") out.push(n.attrs["aria-label"]); for (const c of (n && n.children) || []) labels(c, out); return out; };
const agePiece = (n) => { if (n && n.attrs && n.attrs["data-field"] === "age") return n; for (const c of (n && n.children) || []) { const a = agePiece(c); if (a) return a; } return null; };
location.hash = "#" + hashString({ view: "all" });
state.loaded = true;
renderRows();
process.stdout.write(JSON.stringify({ Class: trs[0].attrs.class, Selected: trs[0].attrs["aria-selected"], AgeTitle: (agePiece(trs[0]) || { attrs: {} }).attrs.title || "",
  Heads: heads(rowHead(cols, lens, unitTitle("all", lens))), Labels: trs.map((tr) => labels(tr)), SlackClass: trs[1].attrs.class.split(" "),
  Count: document.getElementById("list-sum").textContent, Countdown: countdownTitle }));`,
		map[string]any{"pr": wireRow("me", now, pr), "slack": slack, "now": now.UnixMilli()}, &got)
	if !strings.Contains(" "+got.Class+" ", " sel ") || got.Selected != "true" {
		t.Errorf("the open row is %q, aria-selected %q", got.Class, got.Selected)
	}
	if !slices.Contains(got.Heads, "Actions") {
		t.Errorf("heads %q lack the Actions a row serves", got.Heads)
	}
	if got.AgeTitle == "" {
		t.Error("the age piece does not tell its exact time")
	}
	if len(got.Labels[0]) == 0 || !strings.HasPrefix(got.Labels[0][0], "GitHub filter ") {
		t.Errorf("the GitHub row's buttons say %q, want its plugin's words", got.Labels[0])
	}
	for _, l := range got.Labels[1] {
		if strings.HasPrefix(l, "GitHub filter ") {
			t.Errorf("the Slack row says GitHub's words: %q", got.Labels[1])
		}
	}
	if slices.Contains(got.SlackClass, "mine") {
		t.Errorf("GitHub's author row_class marks a Slack row: %v", got.SlackClass)
	}
	if !strings.HasPrefix(got.Countdown, "GitHub sends at ") {
		t.Errorf("the countdown says %q, want its plugin's words", got.Countdown)
	}
	if got.Count != "Item: 2 of 2" {
		t.Errorf("the list summary reads %q", got.Count)
	}
}

// TestTheReviewWalkOffersNoFilterItCannotApply renders a review step's tasks: a step lists what its view
// holds, so its pieces are text and its heads do not sort.
func TestTheReviewWalkOffersNoFilterItCannotApply(t *testing.T) {
	e := task("e-1", "next", func(e *api.TaskView) {
		e.Facets = map[string][]string{"source": {"slack"}, "state": {"next"}, "contexts": {"calls"}}
	})
	var got struct{ Buttons, Text string }
	nodeRun(t, viewsPrelude(t), nil, `
tasksState.catalog = { ...input.cat, review: ["inbox"] };
reviewState.review = { steps: [{ id: "inbox", view: "inbox", label: "Inbox to zero", count: 1 }] };
reviewState.step = "inbox";
tasksState.key = "review|inbox";
tasksState.tasks = [input.task];
const panel = h("div", {}, ...reviewPanel(""));
process.stdout.write(JSON.stringify({ Buttons: all(panel, (x) => x.tag === "button" && ["facet", "th-sort"].includes(x.attrs.class)).length + "", Text: text(panel) }));`,
		map[string]any{"cat": servedViews(t), "task": e}, &got)
	if got.Buttons != "0" {
		t.Errorf("the review step offers %s filter or sort buttons", got.Buttons)
	}
	if !strings.Contains(got.Text, "@calls") {
		t.Errorf("the review step lacks the task's pieces: %s", got.Text)
	}
}
