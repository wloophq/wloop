package api

import "time"

type Summary struct {
	WaitsOn   string    `json:"waits_on"` // "me", a login, "loop", "nobody", ...
	Since     time.Time `json:"since"`
	Stalled   bool      `json:"stalled"`
	Closed    bool      `json:"closed"` // replaces Kind == "done" in Row.In
	OpenItems int       `json:"open_items"`
	Reason    string    `json:"reason"` // one sentence: why it waits
	Line      string    `json:"line"`   // the short status every client shows (menu bar, TUI list)
}

type RowItem struct {
	ID        string    `json:"id"`
	Kind      string    `json:"kind"` // request, question, decision, todo
	Summary   string    `json:"summary"`
	State     string    `json:"state"` // open, resolved
	WaitingOn []string  `json:"waiting_on"`
	Since     time.Time `json:"since"`
}

// Catalog is the route of the served presentation.
const Catalog = "/api/catalog"

type CatalogResponse struct {
	Version    string          `json:"version"` // changes when any list changes; rows do not carry it
	Views      []ViewSpec      `json:"views"`
	Facets     []FacetSpec     `json:"facets"`
	Situations []SituationSpec `json:"situations"`
}

type ViewSpec struct {
	ID       string   `json:"id"` // "needs-me", ...; unique across providers
	Label    string   `json:"label"`
	Provider string   `json:"provider"` // "" for a core view
	Rows     bool     `json:"rows"`     // a list of rows filtered by Row.Views; false: a custom view the client knows (plan, dash, explore, recent)
	Order    int      `json:"order"`
	Surfaces []string `json:"surfaces"` // "web", "tui", "menubar"
	// Unit names the view's rows, such as "Pull request"; "" for a view whose rows mix providers.
	Unit string `json:"unit,omitempty"`
	// UnitSort is the facet the unit column sorts by, such as "repo"; "" sorts by "repo".
	UnitSort string `json:"unit_sort,omitempty"`
}

type FacetSpec struct {
	ID       string            `json:"id"` // "repo", "author", "kind", ...
	Label    string            `json:"label"`
	Provider string            `json:"provider"` // "" applies to every row
	Group    bool              `json:"group"`
	Sort     string            `json:"sort,omitempty"` // "", "rank", "text", "time" (RFC 3339 values)
	Rank     []string          `json:"rank,omitempty"`
	Labels   map[string]string `json:"labels,omitempty"` // value -> shown text
	Empty    string            `json:"empty,omitempty"`
	Match    string            `json:"match,omitempty"` // "exact" (default), "suffix" (repo), "prefix" (tag), "team"
	Menu     bool              `json:"menu"`
	Buckets  []Bucket          `json:"buckets,omitempty"` // Sort "time" only: the client buckets now - value
}

type Bucket struct {
	ID     string `json:"id"` // the value the lens groups and filters by ("today", "week", "due")
	Label  string `json:"label"`
	Within string `json:"within"` // Go duration; the first bucket whose Within covers now - value wins; "" catches the rest
}

type SituationSpec struct {
	Provider string `json:"provider"`
	Kind     Kind   `json:"kind"`
	Label    string `json:"label"`
	Rank     int    `json:"rank"`
}
