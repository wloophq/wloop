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

func TestAMergedFacetKeepsEachProvidersDisplay(t *testing.T) {
	gh := facetPresenter{facets: []api.FacetSpec{{ID: "where", Provider: "github", Group: true, Show: "ref", Labels: map[string]string{"acme/web": "Web"}}}}
	sl := facetPresenter{facets: []api.FacetSpec{{ID: "where", Provider: "slack", Group: true, Show: "meta", LabelPrefix: "#"}}}
	third := facetPresenter{facets: []api.FacetSpec{{ID: "where", Provider: "thread", Group: true}}}
	got := facetByID(readmodel.BuildCatalog(gh, sl, third), "where")
	if len(got) != 1 {
		t.Fatalf("where facets %+v", got)
	}
	m := got[0]
	if g := m.For("github"); g.Show != "ref" || g.LabelPrefix != "" || g.Labels["acme/web"] != "Web" {
		t.Errorf("github places where as %+v", g.Display())
	}
	if s := m.For("slack"); s.Show != "meta" || s.LabelPrefix != "#" {
		t.Errorf("slack places where as %+v", s.Display())
	}
	if th := m.For("thread"); th.Show != "" {
		t.Errorf("thread places where as %+v", th.Display())
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

func TestAMergedFacetKeepsWhichProvidersItIsTheStripOf(t *testing.T) {
	gh := facetPresenter{facets: []api.FacetSpec{{ID: "turn", Provider: "github", Group: true, Strip: true, Colors: map[string]string{"me": "me"}}}}
	sl := facetPresenter{facets: []api.FacetSpec{{ID: "turn", Provider: "slack", Group: true}}}
	got := facetByID(readmodel.BuildCatalog(gh, sl), "turn")
	if len(got) != 1 {
		t.Fatalf("turn facets %+v", got)
	}
	if g := got[0].For("github"); !g.Strip || g.Colors["me"] != "me" {
		t.Errorf("github's turn %+v", g.Display())
	}
	if s := got[0].For("slack"); s.Strip || s.Colors != nil {
		t.Errorf("slack's turn %+v", s.Display())
	}
}

func TestAMergedFacetKeepsEachProvidersBucketWords(t *testing.T) {
	buckets := func(label string) []api.Bucket {
		return []api.Bucket{{ID: "1d", Label: label, Within: "24h"}, {ID: "older", Label: "older"}}
	}
	gh := facetPresenter{facets: []api.FacetSpec{{ID: "age", Provider: "github", Sort: "time", Buckets: buckets("a day")}}}
	sl := facetPresenter{facets: []api.FacetSpec{{ID: "age", Provider: "slack", Sort: "time", Buckets: buckets("today")}}}
	got := facetByID(readmodel.BuildCatalog(gh, sl), "age")
	if len(got) != 1 {
		t.Fatalf("age facets %+v", got)
	}
	if g, s := got[0].For("github"), got[0].For("slack"); g.Buckets[0].Label != "a day" || s.Buckets[0].Label != "today" || s.Buckets[0].Within != "24h" {
		t.Errorf("github's buckets %+v, slack's %+v", g.Buckets, s.Buckets)
	}
}

type reasonPresenter struct {
	facetPresenter
	words api.ProviderWords
}

func (p reasonPresenter) Reasons() api.ProviderWords { return p.words }

func TestTheCatalogServesEachPluginsReasonWords(t *testing.T) {
	words := api.ProviderWords{Provider: "alpha", Words: map[string]string{"asked_by": "{{.actor}} asked"}}
	c := readmodel.BuildCatalog(reasonPresenter{words: words}, reasonPresenter{words: api.ProviderWords{Provider: "beta"}})
	if len(c.Reasons) != 1 || c.Reasons[0].Provider != "alpha" || c.Reasons[0].Words["asked_by"] != "{{.actor}} asked" {
		t.Fatalf("reasons %+v: each plugin that words its reasons serves them", c.Reasons)
	}
}
