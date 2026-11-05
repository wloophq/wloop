package readmodel_test

import (
	"slices"
	"testing"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/readmodel"
)

type baseProvider = readmodel.Provider

type facetPresenter struct {
	baseProvider
	facets []api.FacetSpec
}

func (p facetPresenter) Views() []api.ViewSpec           { return nil }
func (p facetPresenter) Facets() []api.FacetSpec         { return p.facets }
func (p facetPresenter) Situations() []api.SituationSpec { return nil }

func facetByID(c api.CatalogResponse, id string) []api.FacetSpec {
	var out []api.FacetSpec
	for _, f := range c.Facets {
		if f.ID == id {
			out = append(out, f)
		}
	}
	return out
}

func TestFacetsOfTheSameIDMergeWhenTheirMechanicsAgree(t *testing.T) {
	a := facetPresenter{facets: []api.FacetSpec{
		{ID: "where", Label: "Where", Provider: "github", Group: true, Sort: "text", Match: "suffix", Menu: true},
		{ID: "kind", Label: "Situation", Provider: "github"},
	}}
	b := facetPresenter{facets: []api.FacetSpec{{ID: "where", Label: "Where", Provider: "thread", Group: true, Sort: "text", Match: "suffix", Menu: true, Labels: map[string]string{"x": "X"}}}}
	c := readmodel.BuildCatalog(a, b)
	got := facetByID(c, "where")
	if len(got) != 1 || got[0].Provider != "" || got[0].Label != "Where" || got[0].Labels["x"] != "X" {
		t.Fatalf("where facets %+v", got)
	}
	if k := facetByID(c, "kind"); len(k) != 1 || k[0].Provider != "github" {
		t.Errorf("a facet of one provider keeps it: %+v", k)
	}
}

func TestFacetsOfTheSameIDWithOtherMechanicsStayApart(t *testing.T) {
	a := facetPresenter{facets: []api.FacetSpec{{ID: "risk", Provider: "github", Group: true, Sort: "rank"}}}
	b := facetPresenter{facets: []api.FacetSpec{{ID: "risk", Provider: "thread", Group: true, Sort: "text"}}}
	c := readmodel.BuildCatalog(a, b)
	got := facetByID(c, "risk")
	if len(got) != 2 || !slices.Equal([]string{got[0].Provider, got[1].Provider}, []string{"github", "thread"}) {
		t.Fatalf("conflicting facets %+v", got)
	}
}
