package readmodel

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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
		c.Facets = append(c.Facets, pr.Facets()...)
		c.Situations = append(c.Situations, pr.Situations()...)
		if ep, ok := pr.(EventPresenter); ok {
			c.Events = append(c.Events, ep.Events()...)
			c.Details = append(c.Details, ep.Details()...)
		}
	}
	raw, _ := json.Marshal(c)
	sum := sha256.Sum256(raw)
	c.Version = hex.EncodeToString(sum[:6])
	return c
}

// Catalog is the served presentation of the model's providers.
func (m *Model) Catalog() api.CatalogResponse { return BuildCatalog(m.providers...) }
