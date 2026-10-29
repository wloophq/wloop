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
	// Events are the words of the plugins' event kinds.
	Events []EventSpec `json:"events,omitempty"`
	// Details are the words of the fields of the plugins' unit details.
	Details []DetailSpec `json:"details,omitempty"`
}

// DetailSpec is how a field of a plugin's unit detail shows: the field of
// the detail, else of the row's payload override, that holds its value.
type DetailSpec struct {
	Provider string `json:"provider"`
	Field    string `json:"field"`
	Label    string `json:"label"`
	Title    string `json:"title,omitempty"`
}

// SearchSpec is a custom view's search: the queries it runs at a press, the
// terms it toggles, the orders it offers and how a result's decision reads.
type SearchSpec struct {
	DefaultSort string            `json:"default_sort,omitempty"`
	Presets     []SearchPreset    `json:"presets,omitempty"`
	Filters     []SearchFilter    `json:"filters,omitempty"`
	Sorts       []SearchSort      `json:"sorts,omitempty"`
	Decisions   map[string]string `json:"decisions,omitempty"`
	NoDecision  string            `json:"no_decision,omitempty"`
}

type SearchPreset struct {
	Query string `json:"query"`
	Label string `json:"label"`
}

// SearchFilter toggles Term, or keeps Window days of recent activity.
type SearchFilter struct {
	Term   string `json:"term,omitempty"`
	Window int    `json:"window,omitempty"`
	Label  string `json:"label"`
}

type SearchSort struct {
	Sort  string `json:"sort"`
	Label string `json:"label"`
	Shown string `json:"shown,omitempty"`
}

// EventSpec is how a plugin's event kind shows on a timeline; Loop says
// it is the loop's doing.
type EventSpec struct {
	Provider string `json:"provider"`
	Kind     string `json:"kind"`
	Label    string `json:"label"`
	Loop     bool   `json:"loop,omitempty"`
	// Data is how its data shows: "" as it is, "none", "first_field",
	// "short_sha" or "requested".
	Data string `json:"data,omitempty"`
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
	// Search is a custom view's search; nil for none.
	Search *SearchSpec `json:"search,omitempty"`
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
	// Strip says each row's strip is colored by the facet's first value,
	// its rank the strip's legend.
	Strip bool `json:"strip,omitempty"`
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
