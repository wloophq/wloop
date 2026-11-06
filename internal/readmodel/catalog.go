package readmodel

import (
	"maps"
	"slices"

	"github.com/wloophq/wloop/internal/api"
)

// BuildCatalog is the served presentation of the providers that are
// Presenters, in their order; a view id another provider took first is
// left out, and a provider's view orders follow the previous providers'.
func BuildCatalog(providers ...Provider) api.CatalogResponse {
	c := api.CatalogResponse{Views: []api.ViewSpec{}, Facets: []api.FacetSpec{}, Situations: []api.SituationSpec{}}
	for _, p := range providers {
		pr, ok := p.(Presenter)
		if !ok {
			continue
		}
		base := 0
		for _, v := range c.Views {
			base = max(base, v.Order+1)
		}
		for _, v := range pr.Views() {
			if !slices.ContainsFunc(c.Views, func(o api.ViewSpec) bool { return o.ID == v.ID }) {
				v.Order += base
				c.Views = append(c.Views, v)
			}
		}
		for _, f := range pr.Facets() {
			c.Facets = addFacet(c.Facets, f)
		}
		c.Situations = append(c.Situations, pr.Situations()...)
		if ep, ok := pr.(EventPresenter); ok {
			c.Events = append(c.Events, ep.Events()...)
			c.Details = append(c.Details, ep.Details()...)
		}
		if lp, ok := pr.(LensPresenter); ok {
			if w := lp.Lens(); len(w.Words) > 0 {
				c.Lens = append(c.Lens, w)
			}
		}
	}
	c.Version = api.VersionOf(c)
	return c
}

// addFacet adds f to facets: a facet of an id another provider declared, with
// the same mechanics, joins that one as a facet of every row, each provider's
// display keys kept in Displays; with other mechanics it stays apart, for the
// problems to name.
func addFacet(facets []api.FacetSpec, f api.FacetSpec) []api.FacetSpec {
	i := slices.IndexFunc(facets, func(o api.FacetSpec) bool {
		return o.ID == f.ID && o.Provider != f.Provider && o.MechanicsDiff(f) == ""
	})
	if i < 0 {
		return append(facets, f)
	}
	m := &facets[i]
	displays := maps.Clone(m.Displays)
	if displays == nil {
		displays = map[string]api.FacetDisplay{}
		for _, p := range m.Sources() {
			displays[p] = m.Display()
		}
	}
	for _, p := range f.Sources() {
		if d, ok := f.Displays[p]; ok {
			displays[p] = d
		} else {
			displays[p] = f.Display()
		}
	}
	m.Displays = displays
	m.Providers = append(slices.Clone(m.Sources()), f.Sources()...)
	m.Provider = ""
	m.Menu = m.Menu || f.Menu
	m.Strip = m.Strip || f.Strip
	if m.Empty == "" {
		m.Empty = f.Empty
	}
	if len(f.Labels) > 0 {
		m.Labels = maps.Clone(m.Labels)
		if m.Labels == nil {
			m.Labels = map[string]string{}
		}
		for v, label := range f.Labels {
			if _, ok := m.Labels[v]; !ok {
				m.Labels[v] = label
			}
		}
	}
	return facets
}

// Catalog is the served presentation of the model's providers.
func (m *Model) Catalog() api.CatalogResponse { return BuildCatalog(m.providers...) }
