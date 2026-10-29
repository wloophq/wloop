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
