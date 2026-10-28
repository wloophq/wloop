package web

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/api"
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
	whole := api.Counters{Views: map[string]int{"focus": 1, "inbox": 3, "outbox": 0, "next": 3, "waiting": 0, "projects": 0, "someday": 0, "logbook": 4},
		Areas: map[string]int{"OSS": 2, "Hiring": 1}, Contexts: map[string]int{"@decide": 1, "@reply": 2}}
	narrowed := api.Counters{Views: map[string]int{"focus": 0, "inbox": 1, "outbox": 0, "next": 2, "waiting": 0, "projects": 0, "someday": 0, "logbook": 1},
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
const route = { view: "next", area: "", ctx: [], task: "", q: "invoice" };
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
  tasksState.key = viewQueryKey("next", "", []);
  tasksState.tasks = input.next;
  const list = taskList(route, "");
  const filtered = tasksState.filtered;
  process.stdout.write(JSON.stringify({ urls, before, rail, stale, cleared, filtered,
    head: text(byClass(list, "lens-count")),
    groups: all(list, (x) => x.tag === "h3").map(text),
    areas: all(list, (x) => x.tag === "option").map(text),
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
		t.Errorf("rail without free words %v, want %v", got.Cleared, want)
	}
	if want := "/api/counters?term=invoice"; len(got.URLs) == 0 || got.URLs[0] != want {
		t.Errorf("urls %v, want first %s", got.URLs, want)
	}
	if last := got.URLs[len(got.URLs)-1]; last != "/api/counters?area=OSS&term=invoice&term=the+client" {
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
process.stdout.write(JSON.stringify(text(byClass(taskList({ view: "inbox", area: "", ctx: [], task: "", q: "" }, ""), "lens-count"))));`,
		map[string]any{"cat": viewsCatalog(), "tasks": []api.TaskView{task("a", "inbox", nil), task("b", "inbox", nil)}}, &head)
	if head != "2 of 2" {
		t.Errorf("heading %q", head)
	}
}

// TestTheCockpitAndTheDaemonMatchTheSameTasks keeps the cockpit's list
// and the daemon's counts on the same tasks: the daemon matches the terms
// the cockpit sends, trimmed as handleCounters trims them, with
// api.TaskMatches.
func TestTheCockpitAndTheDaemonMatchTheSameTasks(t *testing.T) {
	gh := unit.Unit{Provider: unit.GitHub, Key: "acme/api#12"}
	es := []api.TaskView{
		task("a", "next", func(e *api.TaskView) {
			e.Title, e.Notes = "Ação rápida", "see the runbook"
			e.Props = map[string]json.RawMessage{"area": json.RawMessage(`"OSS"`), "contexts": json.RawMessage(`["@reply"]`), "owner": json.RawMessage(`"Launch"`)}
			e.Units, e.UnitLabels = []unit.Unit{gh, {Provider: "slack", Key: "T1/C9"}}, []string{"", "#deploys thread"}
			e.Waiting = &api.WaitingView{On: []api.WaitingPerson{{Name: "Bruna"}}}
			e.Items = []api.ItemView{{Summary: "answer the question"}}
		}),
		task("b", "inbox", func(e *api.TaskView) {
			e.Kind = "capture"
			e.Cards = []api.CardView{{Card: card.Card{LastHuman: &card.Human{Who: items.Person{Key: "github:octocat"}}, WaitsOn: []items.Person{{Name: "Dora"}}}}}
			e.Message = &api.Message{Text: "Which window?"}
		}),
		task("c", "inbox", func(e *api.TaskView) {
			e.Kind = "draft"
			e.Gates = draftGates(api.Draft{Unit: "slack/T1/C1", Target: "C0REL", Where: "#releases", Text: "Ship it", Reaction: "eyes"})
		}),
		task("d", "next", func(e *api.TaskView) { e.Title = "Škoda deal with Erdős" }),
		task("e", "next", func(e *api.TaskView) { e.Title = "회의 준비, がんばる" }),
		task("f", "next", func(e *api.TaskView) { e.Title, e.Notes = "Send the invoice", "e\u0301clair and CAFÉ" }),
	}
	queries := []string{"acao", "runbook oss", "launch", "@reply", "acme/api#12", "slack:t1/c9", "deploys", "bruna", "question", "octocat",
		"dora", "window", "c0rel", "releases", "ship", "eyes", "slack/t1", "next", "capture", "draft ship", "nobody", `"task a" ação`,
		"Škoda", "skoda", "erdos", "Erdős", "회의", "が", "がんばる", `"invoice `, `" the invoice"`, `"send the"`, "eclair", "éclair", "cafe",
		"repo:acme invoice"}
	var got []struct {
		Terms   []string `json:"terms"`
		Matches []bool   `json:"matches"`
	}
	nodeRun(t, viewsPrelude(t), nil, `
process.stdout.write(JSON.stringify(input.queries.map((q) => {
  const ts = filterTerms(q);
  return { terms: ts, matches: input.tasks.map((e) => taskMatches(e, ts)) };
})));`, map[string]any{"tasks": es, "queries": queries}, &got)
	for i, q := range queries {
		var sent []string
		for _, term := range got[i].Terms {
			if term = strings.TrimSpace(term); term != "" {
				sent = append(sent, term)
			}
		}
		for j, e := range es {
			if want := api.TaskMatches(e, sent); got[i].Matches[j] != want {
				t.Errorf("%q (terms %q) on %s: the list shows %v, the daemon counts %v", q, got[i].Terms, e.ID, got[i].Matches[j], want)
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
})();`, map[string]any{"cat": viewsCatalog(), "counters": api.Counters{Views: map[string]int{"next": 2}}}, &got)
	if got.Rail[3] != 2.0 {
		t.Errorf("rail %v", got.Rail)
	}
	if slices.ContainsFunc(got.Failed, func(c any) bool { return c != "" }) {
		t.Errorf("after a failed read the rail still shows the counts of an earlier filter: %v", got.Failed)
	}
}
