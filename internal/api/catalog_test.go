package api

import (
	"testing"

	"github.com/wloophq/wloop/internal/domain/unit"
)

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

func TestARowsReasonReadsInItsPluginsWords(t *testing.T) {
	c := CatalogResponse{Reasons: []ProviderWords{{Provider: "alpha", Words: map[string]string{"asked_by": "{{.actor}} pediu"}}}}
	row := func(provider, key string) Row {
		return Row{Unit: unit.Unit{Provider: unit.ProviderID(provider), Key: "k"}, Summary: Summary{Reason: "Ana asked", ReasonKey: key, ReasonParams: map[string]string{"actor": "Ana"}}}
	}
	for _, c2 := range []struct {
		r    Row
		want string
	}{{row("alpha", "asked_by"), "Ana pediu"}, {row("alpha", "other"), "Ana asked"}, {row("beta", "asked_by"), "Ana asked"}, {row("alpha", ""), "Ana asked"}} {
		if got := c.ReasonOf(c2.r); got != c2.want {
			t.Errorf("%+v reads %q, want %q", c2.r.Summary, got, c2.want)
		}
	}
}
