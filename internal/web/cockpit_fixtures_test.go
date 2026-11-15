package web

import (
	"encoding/json"
	"flag"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/api/lens"
	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/brand/brandtest"
	"github.com/wloophq/wloop/internal/domain/card"
	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/i18n"
	"github.com/wloophq/wloop/internal/readmodel/overlay"
)

var updateCockpitFixtures = flag.Bool("update-cockpit-fixtures", false, "rewrite internal/web/testdata/cockpit.fixtures.json")

// cockpitFixtures is what the cockpit's vitest suite reads of the Go side: the served views and catalogs, rows
// with the facets the GitHub provider derives, and what Go's lens and plural rules answer for the cases the
// cockpit must agree on.
const cockpitFixtures = "testdata/cockpit.fixtures.json"

var cockpitFixturesNow = time.Date(2026, 10, 14, 15, 0, 0, 0, time.UTC)

// TestTheCockpitFixturesAreWhatGoServes holds the cockpit's fixtures to what the Go side builds and answers;
// -update-cockpit-fixtures rewrites them.
func TestTheCockpitFixturesAreWhatGoServes(t *testing.T) {
	got, err := json.MarshalIndent(buildCockpitFixtures(t), "", " ")
	if err != nil {
		t.Fatal(err)
	}
	got = append(got, '\n')
	if *updateCockpitFixtures {
		out := string(got)
		if strings.Contains(out, brand.Name) || strings.Contains(out, brand.Command) {
			brandtest.CanUpdate(t)
			out = brandtest.Collapse(out)
		}
		if err := os.WriteFile(cockpitFixtures, []byte(out), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(cockpitFixtures)
	if err != nil {
		t.Fatal(err)
	}
	if brandtest.Expand(string(want)) != string(got) {
		t.Fatalf("%s is stale: go test ./internal/web -run TestTheCockpitFixturesAreWhatGoServes -update-cockpit-fixtures rewrites it", cockpitFixtures)
	}
}

func buildCockpitFixtures(t *testing.T) map[string]any {
	now := cockpitFixturesNow
	views := servedViews(t)
	return map[string]any{
		"apiVersion":  api.Version,
		"now":         now.Format(time.RFC3339),
		"views":       views,
		"bundled":     bundledCatalog(t),
		"lensRows":    wireRows("me", now, lensRows()...),
		"tagRows":     wireRows("me", now, tagRows()...),
		"typeRows":    wireRows("me", now, typeRows()...),
		"noneTagRows": wireRows("me", now, noneTagRows()...),
		"waitCases":   waitCases(now),
		"tipRows":     wireRows("me", now, tipRows()...),
		"loopRows":    wireRows("me", now, loopRows()...),
		"filterRows":  filterRows(now),
		"taskMatch":   taskMatch(t, views, now),
		"plurals":     plurals(),
	}
}

func typeRows() []map[string]any {
	rows := lensRows()
	for i, kind := range []string{"feature", "fix", "", "chore", "feature"} {
		if b, _ := rows[i]["brief"].(map[string]any); b != nil && kind != "" {
			b["kind"] = kind
		}
	}
	return rows
}

func noneTagRows() []map[string]any {
	rows := lensRows()[:3]
	rows[0]["brief"].(map[string]any)["tags"] = []string{"lang:none"}
	rows[1]["brief"].(map[string]any)["tags"] = []string{"lang:go"}
	return rows
}

func waitCases(now time.Time) []map[string]any {
	var cases []map[string]any
	for _, kind := range append([]string{"loop_working", "loop_waiting", "done"}, stalledKinds...) {
		for _, waiting := range []string{"me", "author", "alice", "team:o/t", "author, alice", "nobody", ""} {
			for _, author := range []string{"me", "ana"} {
				cases = append(cases, wireRow("me", now, map[string]any{"label": "o/r#1", "unit": map[string]any{"provider": "github", "key": "o/r#1"},
					"kind": kind, "waiting_on": waiting, "author": author}))
			}
		}
	}
	return append(cases, map[string]any{"unit": map[string]any{"provider": "thread", "key": "c1"}, "kind": "waiting", "summary": map[string]any{"waits_on": "ana"}})
}

func tipRows() []map[string]any {
	row := func(n int, author, kind, waiting, reason string) map[string]any {
		return map[string]any{"ref": map[string]any{"owner": "acme", "repo": "api", "number": n}, "author": author, "kind": kind, "waiting_on": waiting, "reason": reason,
			"views": []string{"mine"}}
	}
	return []map[string]any{
		row(1, "me", "changes", "author", "changes requested by bia"),
		row(2, "ana", "changes", "author", "changes requested by bia"),
		row(3, "ana", "review", "bia, team:acme/core", ""),
		row(4, "ana", "loop_working", "me", "a review round runs"),
		row(5, "ana", "draft", "nobody", ""),
	}
}

func loopRows() []map[string]any {
	working, waiting := lensRows()[4], lensRows()[4]
	working["kind"], working["waiting_on"] = overlay.KindLoopWorking, overlay.WaitsOnMe
	waiting["kind"], waiting["waiting_on"] = overlay.KindLoopWaiting, overlay.WaitsOnMe
	pr := tagRows()[1]
	pr["views"] = []string{"mine"}
	return []map[string]any{working, waiting, pr}
}

func filterRows(now time.Time) map[string]any {
	pull := wireRow("me", now, map[string]any{"title": "Batch export", "label": "acme/api#7", "unit": map[string]any{"provider": "github", "key": "acme/api#7"},
		"repo": "acme/api", "author": "ana", "search": []string{"ana/batch-x"}, "waiting_on": "bob"})
	pull["link"] = "https://github.com/acme/api/pull/7"
	search := wireRow("me", now, map[string]any{"title": "Batch export", "label": "acme/api#7", "unit": map[string]any{"provider": "github", "key": "acme/api#7"},
		"search": []string{"Retry the webhook delivery"}})
	reason := wireRow("me", now, map[string]any{"title": "Batch export", "label": "acme/api#7", "unit": map[string]any{"provider": "github", "key": "acme/api#7"}})
	sum := reason["summary"].(map[string]any)
	sum["reason"], sum["reason_key"] = "no reviewer requested", "no_reviewer_requested"
	return map[string]any{"pull": pull, "search": search, "reason": reason}
}

func taskMatch(t *testing.T, views api.ViewsCatalog, now time.Time) map[string]any {
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
		task("f", "next", func(e *api.TaskView) { e.Title, e.Notes = "Send the invoice", "éclair and CAFÉ" }),
		task("g", "inbox", func(e *api.TaskView) {
			e.Title, e.Facets = "Answer the thread", map[string][]string{"where": {"general"}, "source": {"slack"}, "ci": {"failure"}}
		}),
	}
	queries := []string{"acao", "runbook oss", "launch", "@reply", "acme/api#12", "slack:t1/c9", "deploys", "bruna", "question", "octocat",
		"dora", "window", "c0rel", "releases", "ship", "eyes", "slack/t1", "next", "capture", "draft ship", "nobody", `"task a" ação`,
		"Škoda", "skoda", "erdos", "Erdős", "회의", "が", "がんばる", `"invoice `, `" the invoice"`, `"send the"`, "eclair", "éclair", "cafe",
		"where:acme invoice", "general", "failure thread"}
	specs := taskSpecs(t, views)
	want := make([][]bool, len(queries))
	for i, q := range queries {
		parsed := lens.Parse(strings.TrimSpace(q), specs)
		for _, e := range es {
			want[i] = append(want[i], parsed.Match(lens.Item{Facets: e.Facets, Texts: e.Texts()}, specs, now))
		}
	}
	return map[string]any{"tasks": es, "queries": queries, "want": want}
}

func plurals() map[string]any {
	counts := []float64{0, 1, 2, 5, 1.5, 21, 101, 1000000}
	locales := []string{"en", "pt-BR", "fr", "ru", "ar"}
	forms := i18n.Forms{"one": "one", "other": "other"}
	texts, categories := map[string][]string{}, map[string][]string{}
	for _, l := range locales {
		for _, n := range counts {
			texts[l] = append(texts[l], forms.For(l, n))
			categories[l] = append(categories[l], i18n.Form(l, n))
		}
	}
	return map[string]any{"counts": counts, "locales": locales, "texts": texts, "categories": categories}
}
