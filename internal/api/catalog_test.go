package api

import "testing"

func TestFacetMechanicsDiffNamesWhatDiffers(t *testing.T) {
	one := FacetSpec{ID: "opened", Sort: "time", Buckets: []Bucket{{ID: "1d", Within: "24h"}, {ID: "older"}}}
	other := one
	other.Buckets = []Bucket{{ID: "1d", Within: "48h"}, {ID: "older"}}
	other.Label = "Other label"
	switch {
	case one.MechanicsDiff(other) == "":
		t.Error("buckets with another within are another mechanic")
	case one.MechanicsDiff(one) != "":
		t.Error("a facet differs from itself")
	case (FacetSpec{Match: "exact"}).MechanicsDiff(FacetSpec{}) != "":
		t.Error("exact is the default match")
	}
}

func TestAProvidersRowWordsReplaceTheProfilesForItsRows(t *testing.T) {
	base := LensWords{RowWords: RowWords{ListEmpty: "Links", Stale: "stale"}}
	c := CatalogResponse{Lens: []ProviderWords{{Provider: "github", Words: map[string]string{"list_empty": "Workspace"}}}}
	if got := c.RowWordsOf("github", base); got.ListEmpty != "Workspace" || got.Stale != "stale" {
		t.Errorf("github's words %+v", got.RowWords)
	}
	if got := c.RowWordsOf("slack", base); got.ListEmpty != "Links" {
		t.Errorf("slack's words %+v", got.RowWords)
	}
}
