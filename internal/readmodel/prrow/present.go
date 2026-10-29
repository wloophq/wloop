package prrow

import (
	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/readmodel"
)

var _ readmodel.Presenter = (*Provider)(nil)

// Views are the GitHub plugin's views, as its manifest declares them.
func (p *Provider) Views() []api.ViewSpec {
	if p.src.Catalog == nil {
		return nil
	}
	return p.src.Catalog.Views()
}

// Events are the words of the GitHub plugin's event kinds.
func (p *Provider) Events() []api.EventSpec {
	if ep, ok := p.src.Catalog.(readmodel.EventPresenter); ok {
		return ep.Events()
	}
	return nil
}

// Situations are the GitHub kinds with their labels, ranked as the
// cockpit orders them.
func (p *Provider) Situations() []api.SituationSpec {
	if p.src.Catalog == nil {
		return nil
	}
	return p.src.Catalog.Situations()
}

// Facet ids of the GitHub rows.
const (
	FacetRepo    = "repo"
	FacetAuthor  = "author"
	FacetWait    = "wait"
	FacetWaitsOn = "waits-on"
	FacetKind    = "kind"
	FacetType    = "type"
	FacetRisk    = "risk"
	FacetSize    = "size"
	FacetStatus  = "status"
	FacetTag     = "tag"
	FacetIs      = "is"
	FacetCI      = "ci"
	FacetOpened  = "opened"
	FacetScope   = "scope"
	FacetAge     = "age"
	FacetLane    = "lane"
	FacetDue     = "due"
)

var (
	sizes     = []string{"xs", "s", "m", "l", "xl"}
	risks     = []string{"low", "medium", "high"}
	waitOrder = []string{"me", "loop", "reviewer", "author", "nobody"}
	// flags are the clock-free flags of facet is, in the cockpit's order.
	flags = []string{"stalled", "needs-me", "in-loop", "approved", "paused", "muted", "adopted", "tracked"}
)

// Facets are the GitHub plugin's facets, as its manifest declares them,
// then the brief intent's enum extras.
func (p *Provider) Facets() []api.FacetSpec {
	var out []api.FacetSpec
	if p.src.Catalog != nil {
		out = p.src.Catalog.Facets()
	}
	if p.src.Settings == nil {
		return out
	}
	return append(out, ExtraFacetSpecs(registryOf(p.src.Settings().Loaded))...)
}
