package web

import (
	"slices"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/domain/card"
	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/unit"
)

func TestTheFilterNarrowsTheGTDCounts(t *testing.T) {
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
	next := []api.EntryView{
		entry("n1", "next", func(e *api.EntryView) { e.Title, e.Area, e.Contexts = "Invoice the client", "OSS", []string{"@reply"} }),
		entry("n2", "next", func(e *api.EntryView) {
			e.Title, e.Area, e.Contexts = "Review invoice terms", "OSS", []string{"@reply"}
		}),
		entry("n3", "next", func(e *api.EntryView) { e.Title, e.Contexts = "Call Bob", []string{"@decide"} }),
	}
	nodeRun(t, gtdPrelude(t), nil, `
function writeHash() {}
const urls = [];
let answer = null;
async function getJSON(url) { urls.push(url); return answer; }
gtdState.catalog = input.cat;
gtdState.counters = input.whole;
const counts = (route) => gtdNav(gtdState.catalog, [], [], gtdCounters(route)).map((v) => v.count);
const route = { view: "next", area: "", ctx: [], entry: "", q: "invoice" };
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
  gtdState.key = gtdQueryKey("next", "", []);
  gtdState.entries = input.next;
  const list = gtdList(route, "");
  const filtered = gtdState.filtered;
  process.stdout.write(JSON.stringify({ urls, before, rail, stale, cleared, filtered,
    head: text(byClass(list, "lens-count")),
    groups: all(list, (x) => x.tag === "h3").map(text),
    areas: all(list, (x) => x.tag === "option").map(text),
    chips: byClass(list, "chip").map(text) }));
})();`, map[string]any{"cat": gtdCatalog(), "whole": whole, "narrowed": narrowed, "next": next}, &got)
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
	if !slices.Equal(got.Groups, []string{"@reply (2)"}) {
		t.Errorf("groups %v", got.Groups)
	}
	if got.Filtered == nil || got.Filtered["area"] != "OSS" {
		t.Errorf("the last read is the OSS one: %v", got.Filtered)
	}
	if strings.Join(got.Areas, ",") != "All areas,OSS,Hiring" || !slices.Equal(got.Chips, []string{"@decide", "@reply"}) {
		t.Errorf("the OSS counts are not this route's, so the picker and the chips show none: areas %v chips %v", got.Areas, got.Chips)
	}
}

func TestTheGTDHeadingCountsTheWholeViewWithoutAFilter(t *testing.T) {
	var head string
	nodeRun(t, gtdPrelude(t), nil, `
function writeHash() {}
gtdState.catalog = input.cat;
gtdState.key = gtdQueryKey("inbox", "", []);
gtdState.entries = input.entries;
process.stdout.write(JSON.stringify(text(byClass(gtdList({ view: "inbox", area: "", ctx: [], entry: "", q: "" }, ""), "lens-count"))));`,
		map[string]any{"cat": gtdCatalog(), "entries": []api.EntryView{entry("a", "inbox", nil), entry("b", "inbox", nil)}}, &head)
	if head != "2 of 2" {
		t.Errorf("heading %q", head)
	}
}

// TestTheCockpitAndTheDaemonMatchTheSameEntries keeps entryMatches and
// api.EntryMatches reading the same texts, so a rail count the daemon
// narrows agrees with the list the cockpit narrows.
func TestTheCockpitAndTheDaemonMatchTheSameEntries(t *testing.T) {
	gh := unit.Unit{Provider: unit.GitHub, Key: "acme/api#12"}
	es := []api.EntryView{
		entry("a", "next", func(e *api.EntryView) {
			e.Title, e.Notes, e.Area, e.ProjectTitle, e.Contexts = "Ação rápida", "see the runbook", "OSS", "Launch", []string{"@reply"}
			e.Units, e.UnitLabels = []unit.Unit{gh, {Provider: "slack", Key: "T1/C9"}}, []string{"", "#deploys thread"}
			e.Waiting = &api.WaitingView{On: []api.WaitingPerson{{Name: "Bruna"}}}
			e.Items = []api.ItemView{{Summary: "answer the question"}}
		}),
		entry("b", "inbox", func(e *api.EntryView) {
			e.Kind = "capture"
			e.Cards = []api.CardView{{Card: card.Card{LastHuman: &card.Human{Who: items.Person{Key: "github:octocat"}}, WaitsOn: []items.Person{{Name: "Dora"}}}}}
			e.Message = &api.Message{Text: "Which window?"}
		}),
		entry("c", "inbox", func(e *api.EntryView) {
			e.Kind = "draft"
			e.Draft = &api.Draft{Unit: "slack/T1/C1", Target: "C0REL", Where: "#releases", Text: "Ship it", Reaction: "eyes"}
		}),
	}
	queries := [][]string{{"acao"}, {"runbook", "oss"}, {"launch"}, {"@reply"}, {"acme/api#12"}, {"slack:t1/c9"}, {"deploys"}, {"bruna"},
		{"question"}, {"octocat"}, {"dora"}, {"window"}, {"c0rel"}, {"releases"}, {"ship"}, {"eyes"}, {"slack/t1"}, {"next"}, {"capture"},
		{"draft", "ship"}, {"nobody"}, {"entry a", "ação"}}
	var got [][]bool
	nodeRun(t, gtdPrelude(t), nil, `
process.stdout.write(JSON.stringify(input.queries.map((ts) => input.entries.map((e) => entryMatches(e, ts.map(gtdFold))))));`,
		map[string]any{"entries": es, "queries": queries}, &got)
	for i, ts := range queries {
		for j, e := range es {
			if want := api.EntryMatches(e, ts); got[i][j] != want {
				t.Errorf("terms %v on %s: cockpit %v, daemon %v", ts, e.ID, got[i][j], want)
			}
		}
	}
}
