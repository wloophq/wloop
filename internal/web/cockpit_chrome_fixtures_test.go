package web

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/pelletier/go-toml/v2"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/brand/brandtest"
	"github.com/wloophq/wloop/internal/domain/unit"
)

// cockpitChromeFixtures is what the vitest suite of the cockpit's chrome reads of the Go side: the served catalogs
// and views, rows and tasks as the daemon serves them, the action specs and the profile's strings.
const cockpitChromeFixtures = "testdata/cockpit.chrome.json"

// TestTheChromeFixturesAreWhatGoServes holds the chrome's fixtures to what the Go side builds;
// -update-cockpit-fixtures rewrites them.
func TestTheChromeFixturesAreWhatGoServes(t *testing.T) {
	got, err := json.MarshalIndent(buildChromeFixtures(t), "", " ")
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
		if err := os.WriteFile(cockpitChromeFixtures, []byte(out), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(cockpitChromeFixtures)
	if err != nil {
		t.Fatal(err)
	}
	if brandtest.Expand(string(want)) != string(got) {
		t.Fatalf("%s is stale: go test ./internal/web -run TestTheChromeFixturesAreWhatGoServes -update-cockpit-fixtures rewrites it", cockpitChromeFixtures)
	}
}

func profileStrings(t *testing.T, path string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Strings map[string]any `toml:"strings"`
	}
	if err := toml.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	return doc.Strings
}

func buildChromeFixtures(t *testing.T) map[string]any {
	var catalog any
	if err := json.Unmarshal([]byte(servedCatalog(t)), &catalog); err != nil {
		t.Fatal(err)
	}
	specs, rows, _, _ := wireFixtures(t)
	dashNow := time.Date(2026, 10, 19, 15, 0, 0, 0, time.UTC)
	copyRow := wireRows("carol", tasklensNow, map[string]any{
		"label": "acme/widgets#4870", "unit": map[string]any{"provider": "github", "key": "acme/widgets#4870"}, "link": "https://github.com/acme/widgets/pull/4870",
		"title": "perf: one \"conn\"\nper batch", "author": "carol", "kind": "conflicts", "waiting_on": "author", "since": "2026-10-13T13:00:00Z",
		"stalled": true, "reason": "merge conflicts with develop", "head": "fix/x", "reasons": []string{"author"},
		"workspace": map[string]any{"display_name": "fix-x", "status": "in-progress"},
		"brief":     map[string]any{"size": "m", "risk": "high", "goal": "Run batches on one connection.", "open_questions": 2},
		"override":  map[string]any{"hold": true},
	})
	copyTask := task("t-1", "next", func(e *api.TaskView) {
		e.Title, e.Units, e.UnitLabels = "Answer the deploy question", []unit.Unit{{Provider: "slack", Key: "T1/C1/1"}}, []string{"#general thread"}
		e.Facets = map[string][]string{"where": {"general"}, "source": {"slack"}, "asked-by": {}}
	})
	return map[string]any{
		"catalog":     catalog,
		"views":       servedViews(t),
		"railViews":   viewsCatalog(),
		"slackFacets": slackFacets(t),
		"mixedTasks":  mixedTasks(),
		"mineRows":    mineRows(),
		"lensNow":     tasklensNow.UnixMilli(),
		"dashNow":     dashNow.UnixMilli(),
		"dashRows":    wireRows("me", dashNow, dashRows(dashNow)...),
		"copyRow":     copyRow[0],
		"copyTask":    copyTask,
		"quietClose":  quietClose(),
		"stringsPtBR": profileStrings(t, "../../profiles/default/locales/pt-BR/strings.toml"),
		"stringsEn":   profileStrings(t, "../../profiles/default/strings.toml"),
		"specs":       specs,
		"usersRow":    usersRowInput(t),
		"candidates":  map[string]any{"explore_candidate_mine": rows["explore_candidate_mine"], "untracked_open_not_mine": rows["untracked_open_not_mine"]},
	}
}

// usersRowInput is the row a user saw broken in My PRs, with the action specs and the served catalog in pt-BR words
// it is drawn with; the row's vitest suite draws it for the layout tests.
func usersRowInput(t *testing.T) map[string]any {
	t.Helper()
	pr := lensRows()[1]
	pr["kind"], pr["waiting_on"], pr["stalled"] = "no_reviewer", "me", true
	pr["title"] = "fix(parts): resolve messages delivered out of order by the unary ack consumer when the broker redelivers"
	pr["brief"] = map[string]any{"risk": "medium", "size": "m", "kind": "fix", "open_questions": 2, "tags": []string{"area:messaging", "audience:developers",
		"audience:operators", "change:fix", "concern:concurrency", "concern:reliability", "domain:parts-matching", "lang:python", "tool:gcp-pubsub",
		"infra:queue", "infra:db", "prio:high", "customer:acme"}}
	pr["views"] = []string{"mine", "stalled"}
	actions := []map[string]any{}
	specs := map[string]any{}
	for _, a := range [][2]string{{"review", "Review"}, {"address", "Address"}, {"act", "Agir"}, {"nudge", "Cutucar"}, {"pause", "Pausar"}, {"mute", "Silenciar"},
		{"open", "Abrir"}, {"remove", "Remover"}} {
		actions = append(actions, map[string]any{"id": a[0], "enabled": true})
		specs[a[0]] = map[string]any{"id": a[0], "label": a[1], "group": "control", "surfaces": []string{"row"}}
	}
	pr["actions"] = actions
	now := time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC)
	var cat map[string]any
	if err := json.Unmarshal([]byte(servedCatalog(t)), &cat); err != nil {
		t.Fatal(err)
	}
	ptBR(cat)
	return map[string]any{"row": wireRow("me", now, pr), "specs": specs, "catalog": cat, "now": now.UnixMilli()}
}

// ptBR words the served catalog's situation of no reviewer as the user's locale does.
func ptBR(cat map[string]any) {
	for _, s := range cat["situations"].([]any) {
		if sit := s.(map[string]any); sit["kind"] == "no_reviewer" {
			sit["label"] = "sem revisor"
		}
	}
	for _, f := range cat["facets"].([]any) {
		if spec := f.(map[string]any); spec["id"] == "kind" {
			labels, _ := spec["labels"].(map[string]any)
			if labels == nil {
				labels = map[string]any{}
				spec["labels"] = labels
			}
			labels["no_reviewer"] = "sem revisor"
		}
	}
}
