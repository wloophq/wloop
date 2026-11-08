package source_test

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/wloophq/wloop/internal/api"
	psource "github.com/wloophq/wloop/internal/plugins/source"
)

func bundledCatalog(t *testing.T, id string) psource.Catalog {
	t.Helper()
	c := psource.BundledCatalog(id)
	if c.Source == nil {
		t.Fatalf("no bundled %s", id)
	}
	return c
}

// TestGitHubsCatalogIsItsManifestWordedByItsLayer holds the catalog the
// GitHub plugin's manifest and ui.toml serve to the one the host's Go
// tables served before they moved.
func TestGitHubsCatalogIsItsManifestWordedByItsLayer(t *testing.T) {
	c := bundledCatalog(t, "github")
	got := struct {
		Views      []api.ViewSpec      `json:"views"`
		Facets     []api.FacetSpec     `json:"facets"`
		Situations []api.SituationSpec `json:"situations"`
	}{c.Views(), c.Facets(), c.Situations()}
	raw, err := json.MarshalIndent(got, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile("testdata/github.catalog.golden.json")
	if err != nil {
		t.Fatal(err)
	}
	if string(raw)+"\n" != string(want) {
		os.WriteFile("testdata/github.catalog.got.json", raw, 0o644)
		t.Fatal("the GitHub catalog differs from testdata/github.catalog.golden.json; see testdata/github.catalog.got.json")
	}
}

func TestAPluginsUIIsItsManifestsWordedByItsLayer(t *testing.T) {
	ui := bundledCatalog(t, "github").PluginUI()
	if ui == nil || ui.Key != "g g" || len(ui.Menubar) != 0 {
		t.Fatalf("ui %+v", ui)
	}
	if len(ui.Views) == 0 || ui.Views[0] != (api.SavedView{ID: "mine", Label: "My PRs", View: "mine"}) {
		t.Fatalf("views %+v", ui.Views)
	}
	if (psource.Catalog{}).PluginUI() != nil {
		t.Fatal("a manifest with no [source.ui] serves a ui")
	}
}

func TestAPluginsEventKindsAreWordedByItsLayer(t *testing.T) {
	events := bundledCatalog(t, "github").Events()
	byKind := map[string]api.EventSpec{}
	for _, e := range events {
		byKind[e.Kind] = e
	}
	if e := byKind["round_sent"]; e.Label != "round sent" || !e.Loop || e.Provider != "github" {
		t.Fatalf("round_sent %+v", e)
	}
	if e := byKind["push"]; e.Label != "pushed" || e.Loop {
		t.Fatalf("push %+v", e)
	}
}

func TestAFacetRankedBySituationsReadsAsTheSituationsDo(t *testing.T) {
	for _, c := range []struct{ plugin, facet string }{{"github", "kind"}, {"slack", "situation"}} {
		cat := bundledCatalog(t, c.plugin)
		labels := map[string]map[string]string{}
		for _, f := range cat.Facets() {
			labels[f.ID] = f.Labels
		}
		for _, s := range cat.Situations() {
			if got := labels[c.facet][string(s.Kind)]; got != s.Label {
				t.Errorf("%s's %s reads %s as %q, its situation as %q", c.plugin, c.facet, s.Kind, got, s.Label)
			}
			for id, ls := range labels {
				if id != c.facet && ls[string(s.Kind)] == s.Label && s.Label != string(s.Kind) {
					t.Errorf("%s's %s, not ranked by situations, reads %s as its situation", c.plugin, id, s.Kind)
				}
			}
		}
	}
}

// TestGitHubsFacetsWordTheirKnownValues checks that every value the GitHub plugin's rows show by a fixed id
// is worded, so no row reads a raw id: the brief's risk and the viewer as author or waiter.
func TestGitHubsFacetsWordTheirKnownValues(t *testing.T) {
	want := map[string][]string{"risk": {"low", "medium", "high"}, "author": {"me"}, "waits-on": {"me"}}
	for _, f := range bundledCatalog(t, "github").Facets() {
		for _, v := range want[f.ID] {
			if f.Labels[v] == "" {
				t.Errorf("facet %s shows value %q raw", f.ID, v)
			}
		}
	}
}

func TestGitHubsReasonsAreWordedByItsLayer(t *testing.T) {
	w := bundledCatalog(t, "github").Reasons()
	if w.Provider != "github" || w.Words["no_reviewer_requested"] != "no reviewer requested" || w.Words["changes_requested"] != "changes requested by {{.actor}}" {
		t.Fatalf("reasons %+v", w)
	}
}

func TestGitHubsCardSignalsAreWordedByItsLayer(t *testing.T) {
	by := map[string]api.SignalSpec{}
	for _, s := range bundledCatalog(t, "github").CardSignals() {
		if s.Provider != "github" {
			t.Errorf("signal %+v of another provider", s)
		}
		by[s.Key] = s
	}
	for key, want := range map[string]string{"situation": "situation", "checks": "checks", "review": "review", "merge": "merge"} {
		if by[key].Label != want {
			t.Errorf("signal %s labeled %q, want %q", key, by[key].Label, want)
		}
	}
	for _, c := range []struct{ key, code, want string }{
		{"situation", "changes", "changes asked"},
		{"situation", "loop_working", "agent working"},
		{"checks", "passing", "passing"},
		{"checks", "failing_named", "failing: {{.names}}"},
		{"merge", "conflicts", "conflicts"},
	} {
		if got := by[c.key].Values[c.code]; got != c.want {
			t.Errorf("%s=%s reads %q, want %q", c.key, c.code, got, c.want)
		}
	}
	if got := by["situation"].Notes["stalled"]; got != `stalled for {{.days}} {{plural .days "one" "working day" "other" "working days"}}` {
		t.Errorf("the stalled note reads %q", got)
	}
}
