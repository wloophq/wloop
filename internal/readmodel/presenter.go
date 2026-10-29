package readmodel

import "github.com/wloophq/wloop/internal/api"

// Presenter is optional on a Provider: its served views, facets and situations, read once per catalog build.
type Presenter interface {
	Views() []api.ViewSpec
	Facets() []api.FacetSpec
	Situations() []api.SituationSpec
}

// EventPresenter is optional on a Presenter: the words of its event kinds.
type EventPresenter interface {
	Events() []api.EventSpec
}
