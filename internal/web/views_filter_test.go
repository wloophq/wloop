package web

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/api/lens"
	"github.com/wloophq/wloop/internal/domain/card"
	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/unit"
)

func TestTheFilterNarrowsTheViewCounts(t *testing.T) {
	var got struct {
		URLs     []string       `json:"urls"`
		Before   []any          `json:"before"`
		Rail     []any          `json:"rail"`
		Stale    []any          `json:"stale"`
		Cleared  []any          `json:"cleared"`
		Head     string         `json:"head"`
		Groups   []string       `json:"groups"`
		Areas    []string       `json:"areas"`
		Chips    []string       `json:"chips"`
		Filtered map[string]any `json:"filtered"`
	}
	whole := api.Counters{Views: map[string]int{"focus": 1, "inbox": 3, "outbox": 0, "fits-now": 3, "waiting": 0, "projects": 0, "someday": 0, "logbook": 4},
		Areas: map[string]int{"OSS": 2, "Hiring": 1}, Contexts: map[string]int{"@decide": 1, "@reply": 2}}
	narrowed := api.Counters{Views: map[string]int{"focus": 0, "inbox": 1, "outbox": 0, "fits-now": 2, "waiting": 0, "projects": 0, "someday": 0, "logbook": 1},
		Areas: map[string]int{"OSS": 2}, Contexts: map[string]int{"@reply": 2}}
	next := []api.TaskView{
		task("n1", "next", func(e *api.TaskView) {
			e.Title, e.Props = "Invoice the client", map[string]json.RawMessage{"area": json.RawMessage(`"OSS"`), "contexts": json.RawMessage(`["@reply"]`)}
		}),
		task("n2", "next", func(e *api.TaskView) {
			e.Title, e.Props = "Review invoice terms", map[string]json.RawMessage{"area": json.RawMessage(`"OSS"`), "contexts": json.RawMessage(`["@reply"]`)}
		}),
		task("n3", "next", func(e *api.TaskView) {
			e.Title, e.Props = "Call Bob", map[string]json.RawMessage{"contexts": json.RawMessage(`["@decide"]`)}
		}),
	}
	nodeRun(t, viewsPrelude(t), nil, `
function writeHash() {}
const urls = [];
let answer = null;
async function getJSON(url) { urls.push(url); return answer; }
tasksState.catalog = input.cat;
tasksState.counters = input.whole;
const counts = (route) => viewNav(tasksState.catalog, [], [], viewCounters(route)).map((v) => v.count);
const route = { view: "fits-now", area: "", ctx: [], task: "", q: "invoice" };
(async () => {
  const before = counts(route);
  answer = input.narrowed;
  await loadFiltered(route);
  const rail = counts(route);
  answer = input.whole;
  const slow = loadFiltered({ ...route, q: "invoic" });
  answer = input.narrowed;
  await loadFiltered(route);
  await slow;
  const stale = counts(route);
  const cleared = counts({ ...route, q: "" });
  await loadFiltered({ ...route, area: "OSS", q: "invoice \"the client\"" });
  tasksState.key = viewQueryKey("fits-now", "", []);
  tasksState.tasks = input.next;
  const list = taskListNodes(route, "");
  const filtered = tasksState.filtered;
  process.stdout.write(JSON.stringify({ urls, before, rail, stale, cleared, filtered,
    head: text(byClass(list, "lens-count")),
    groups: all(list, (x) => x.tag === "h3").map(text),
    areas: all(all(list, (x) => x.attrs["data-focus"] === "view:area"), (x) => x.tag === "option").map(text),
    chips: byClass(list, "chip").map(text) }));
})();`, map[string]any{"cat": viewsCatalog(), "whole": whole, "narrowed": narrowed, "next": next}, &got)
	if slices.ContainsFunc(got.Before, func(c any) bool { return c != "" }) {
		t.Errorf("before the narrowed counters arrive the rail shows no whole count: %v", got.Before)
	}
	if want := []any{0.0, 1.0, 0.0, 2.0, 0.0, 0.0, 0.0, 1.0}; !slices.Equal(got.Rail, want) {
		t.Errorf("rail under the filter %v, want %v", got.Rail, want)
	}
	if !slices.Equal(got.Stale, got.Rail) {
		t.Errorf("an older answer replaced the latest: %v, want %v", got.Stale, got.Rail)
	}
	if want := []any{1.0, 3.0, 0.0, 3.0, 0.0, 0.0, 0.0, 4.0}; !slices.Equal(got.Cleared, want) {
		t.Errorf("rail with the query cleared %v, want %v", got.Cleared, want)
	}
	if want := "/api/counters?q=invoice"; len(got.URLs) == 0 || got.URLs[0] != want {
		t.Errorf("urls %v, want first %s", got.URLs, want)
	}
	if last := got.URLs[len(got.URLs)-1]; last != "/api/counters?area=OSS&q=invoice+%22the+client%22" {
		t.Errorf("an area and a quoted phrase read %s", last)
	}
	if got.Head != "2 of 3" {
		t.Errorf("the heading counts %q, want 2 of 3", got.Head)
	}
	if len(got.Groups) != 0 {
		t.Errorf("a view that groups by nothing shows groups %v", got.Groups)
	}
	if got.Filtered == nil || got.Filtered["area"] != "OSS" {
		t.Errorf("the last read is the OSS one: %v", got.Filtered)
	}
	if strings.Join(got.Areas, ",") != "All areas,OSS,Hiring" || !slices.Equal(got.Chips, []string{"@decide", "@reply"}) {
		t.Errorf("the OSS counts are not this route's, so the picker and the chips show none: areas %v chips %v", got.Areas, got.Chips)
	}
}

func TestTheViewHeadingCountsTheWholeViewWithoutAFilter(t *testing.T) {
	var head string
	nodeRun(t, viewsPrelude(t), nil, `
function writeHash() {}
tasksState.catalog = input.cat;
tasksState.key = viewQueryKey("inbox", "", []);
tasksState.tasks = input.tasks;
process.stdout.write(JSON.stringify(text(byClass(taskListNodes({ view: "inbox", area: "", ctx: [], task: "", q: "" }, ""), "lens-count"))));`,
		map[string]any{"cat": viewsCatalog(), "tasks": []api.TaskView{task("a", "inbox", nil), task("b", "inbox", nil)}}, &head)
	if head != "2 of 2" {
		t.Errorf("heading %q", head)
	}
}

// TestTheCockpitAndTheDaemonMatchTheSameTasks keeps the cockpit's list and the daemon's counts on the same
// tasks: the daemon reads the query the cockpit sends with the Go lens over the same specs.
func TestTheCockpitAndTheDaemonMatchTheSameTasks(t *testing.T) {
	gh := unit.Unit{Provider: unit.GitHub, Key: "acme/api#12"}
	es := []api.TaskView{
		task("a", "next", func(e *api.TaskView) {
			e.Title, e.Notes = "Ação rápida", "see the runbook"
			e.Props = map[string]json.RawMessage{"area": json.RawMessage(`"OSS"`), "contexts": json.RawMessage(`["@reply"]`), "owner": json.RawMessage(`"Launch"`)}
			e.Units, e.UnitLabels = []unit.Unit{gh, {Provider: "slack", Key: "T1/C9"}}, []string{"", "#deploys thread"}
			e.OwedBy = "Bruna"
			e.Asks = []api.AskView{{Summary: "answer the question"}}
		}),
		task("b", "inbox", func(e *api.TaskView) {
			e.Props = map[string]json.RawMessage{"kind": json.RawMessage(`"capture"`)}
			e.Cards = []api.CardView{{Card: card.Card{LastHuman: &card.Human{Who: items.Person{Key: "github:octocat"}}, WaitsOn: []items.Person{{Name: "Dora"}}}}}
			subject, _ := json.Marshal(api.Message{ID: 2, Text: "Which window?"})
			e.Gates = []api.GateView{{ID: 2, Kind: "question", Status: "open", Intents: []api.IntentView{}, Subject: subject}}
		}),
		task("c", "inbox", func(e *api.TaskView) {
			e.Gates = draftGates(api.Draft{Unit: "slack/T1/C1", Target: "C0REL", Where: "#releases", Text: "Ship it", Reaction: "eyes"})
		}),
		task("d", "next", func(e *api.TaskView) { e.Title = "Škoda deal with Erdős" }),
		task("e", "next", func(e *api.TaskView) { e.Title = "회의 준비, がんばる" }),
		task("f", "next", func(e *api.TaskView) { e.Title, e.Notes = "Send the invoice", "e\u0301clair and CAFÉ" }),
		task("g", "inbox", func(e *api.TaskView) {
			e.Title, e.Facets = "Answer the thread", map[string][]string{"where": {"general"}, "source": {"slack"}, "ci": {"failure"}}
		}),
	}
	queries := []string{"acao", "runbook oss", "launch", "@reply", "acme/api#12", "slack:t1/c9", "deploys", "bruna", "question", "octocat",
		"dora", "window", "c0rel", "releases", "ship", "eyes", "slack/t1", "next", "capture", "draft ship", "nobody", `"task a" ação`,
		"Škoda", "skoda", "erdos", "Erdős", "회의", "が", "がんばる", `"invoice `, `" the invoice"`, `"send the"`, "eclair", "éclair", "cafe",
		"where:acme invoice", "general", "failure thread"}
	views := servedViews(t)
	specs := taskSpecs(t, views)
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	var got [][]bool
	nodeRun(t, viewsPrelude(t), nil, `
Date.now = () => input.now;
tasksState.catalog = input.cat;
const items = input.tasks.map(taskItemOf);
process.stdout.write(JSON.stringify(input.queries.map((q) => {
  const lens = lensOf({ view: "inbox", area: "", ctx: [], q });
  return items.map((it) => matches(it, lens.ts) && passesFilters(it, lens.filters));
})));`, map[string]any{"tasks": es, "queries": queries, "cat": views, "now": now.UnixMilli()}, &got)
	for _, q := range []string{"general", "failure thread"} {
		if i := slices.Index(queries, q); !got[i][len(es)-1] {
			t.Errorf("%q misses the task whose facets hold it", q)
		}
	}
	for i, q := range queries {
		query := strings.TrimSpace(q)
		for j, e := range es {
			if want := lens.Parse(query, specs).Match(lens.Item{Facets: e.Facets, Texts: e.Texts()}, specs, now); got[i][j] != want {
				t.Errorf("%q on %s: the list shows %v, the daemon counts %v", q, e.ID, got[i][j], want)
			}
		}
	}
}

func TestAFailedFilteredReadLeavesTheRailWithoutCounts(t *testing.T) {
	var got struct {
		Rail   []any `json:"rail"`
		Failed []any `json:"failed"`
	}
	nodeRun(t, viewsPrelude(t), nil, `
let fail = false;
async function getJSON() { if (fail) throw new Error("HTTP 500"); return input.counters; }
tasksState.catalog = input.cat;
const counts = (route) => viewNav(tasksState.catalog, [], [], viewCounters(route)).map((v) => v.count);
(async () => {
  await loadFiltered({ area: "", q: "invoice" });
  const rail = counts({ area: "", q: "invoice" });
  fail = true;
  await loadFiltered({ area: "", q: "invoices" });
  process.stdout.write(JSON.stringify({ rail, failed: counts({ area: "", q: "invoices" }) }));
})();`, map[string]any{"cat": viewsCatalog(), "counters": api.Counters{Views: map[string]int{"fits-now": 2}}}, &got)
	if got.Rail[3] != 2.0 {
		t.Errorf("rail %v", got.Rail)
	}
	if slices.ContainsFunc(got.Failed, func(c any) bool { return c != "" }) {
		t.Errorf("after a failed read the rail still shows the counts of an earlier filter: %v", got.Failed)
	}
}
