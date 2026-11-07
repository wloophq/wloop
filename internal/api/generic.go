package api

import (
	"slices"
	"time"
)

type Summary struct {
	WaitsOn   string    `json:"waits_on"` // "me", a login, "loop", "nobody", ...
	Since     time.Time `json:"since"`
	Stalled   bool      `json:"stalled"`
	Closed    bool      `json:"closed"` // replaces Kind == "done" in Row.In
	OpenItems int       `json:"open_items"`
	Reason    string    `json:"reason"` // one sentence: why it waits
	Line      string    `json:"line"`   // the short status every client shows (menu bar, TUI list)
	// ReasonKey names Reason in its plugin's words (CatalogResponse.Reasons), which ReasonParams fill; empty
	// for a reason no plugin words.
	ReasonKey    string            `json:"reason_key,omitempty"`
	ReasonParams map[string]string `json:"reason_params,omitempty"`
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
	// Lens are the plugins' own row words, which replace the views catalog's for their rows.
	Lens []ProviderWords `json:"lens,omitempty"`
	// Reasons are the plugins' words of the reasons their rows name (Summary.ReasonKey), by key.
	Reasons []ProviderWords `json:"reasons,omitempty"`
}

// ProviderWords are a provider's words, by key.
type ProviderWords struct {
	Provider string            `json:"provider"`
	Words    map[string]string `json:"words"`
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

// EventSpec is how an event kind, a plugin's or the core's (Provider ""), shows on a timeline; Loop says it is
// the loop's doing, and Tone is what it is drawn in: "loop", "me", "people" or "".
type EventSpec struct {
	Provider string `json:"provider"`
	Kind     string `json:"kind"`
	Label    string `json:"label"`
	Loop     bool   `json:"loop,omitempty"`
	Tone     string `json:"tone,omitempty"`
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
	// DefaultSort is what the sort menu calls the view's own order.
	DefaultSort string `json:"default_sort,omitempty"`
	// Search is a custom view's search; nil for none.
	Search *SearchSpec `json:"search,omitempty"`
}

type FacetSpec struct {
	ID       string `json:"id"` // "repo", "author", "kind", ...
	Label    string `json:"label"`
	Provider string `json:"provider"` // "" applies to every row
	// Fact is the task fact or record field a profile's task facet reads; "" for a plugin's facet.
	Fact    string            `json:"fact,omitempty"`
	Group   bool              `json:"group"`
	Sort    string            `json:"sort,omitempty"` // "", "rank", "text", "time" (RFC 3339 values)
	Rank    []string          `json:"rank,omitempty"`
	Labels  map[string]string `json:"labels,omitempty"` // value -> shown text
	Empty   string            `json:"empty,omitempty"`
	Match   string            `json:"match,omitempty"` // "exact" (default), "suffix" (repo), "prefix" (tag), "team"
	Menu    bool              `json:"menu"`
	Buckets []Bucket          `json:"buckets,omitempty"` // Sort "time" only: the client buckets now - value
	// Strip says each row's strip is colored by the facet's first value,
	// its rank the strip's legend.
	Strip bool `json:"strip,omitempty"`
	// Colors are the strip hues of the facet's values (builtin.StripColors names them), by value.
	Colors map[string]string `json:"colors,omitempty"`
	// Show is the part of a row the facet's values show in (builtin.ShowParts names them); "" keeps the facet to the lens.
	Show string `json:"show,omitempty"`
	// ShowOnly are the values the part shows, all of them when empty.
	ShowOnly []string `json:"show_only,omitempty"`
	// ShowValues is how many of an item's values the part shows: "all" (the default) or "first".
	ShowValues string `json:"show_values,omitempty"`
	// LabelPrefix goes before each shown value's label on a row, never in a group's head.
	LabelPrefix string `json:"label_prefix,omitempty"`
	// ShowWhen names, by facet id, the values a row must hold in that facet for this facet to show; empty
	// shows it always.
	ShowWhen map[string][]string `json:"show_when,omitempty"`
	// ShowMax is how many of the values the part shows at most; the rest count into one piece. 0 shows all.
	ShowMax int `json:"show_max,omitempty"`
	// ShowLabel is an age part's label, a template over `{{.age}}`, which Parts fills, and `{{.at}}`, the
	// raw time a client writes in its own locale.
	ShowLabel string `json:"show_label,omitempty"`
	// RowClass is the class of a row that holds a value, by value.
	RowClass map[string]string `json:"row_class,omitempty"`
	// Displays are, on a facet merged across providers, each provider's display keys, by provider: a row
	// is placed with its own provider's.
	Displays map[string]FacetDisplay `json:"displays,omitempty"`
	// Providers are the providers a merged facet (Provider "") came from; never served.
	Providers []string `json:"-"`
}

// FacetDisplay is where and how one provider shows a facet's values on its rows: FacetSpec's keys of the
// same names.
type FacetDisplay struct {
	Show        string              `json:"show,omitempty"`
	ShowOnly    []string            `json:"show_only,omitempty"`
	ShowValues  string              `json:"show_values,omitempty"`
	LabelPrefix string              `json:"label_prefix,omitempty"`
	ShowWhen    map[string][]string `json:"show_when,omitempty"`
	ShowMax     int                 `json:"show_max,omitempty"`
	ShowLabel   string              `json:"show_label,omitempty"`
	RowClass    map[string]string   `json:"row_class,omitempty"`
	Labels      map[string]string   `json:"labels,omitempty"`
	Strip       bool                `json:"strip,omitempty"`
	Colors      map[string]string   `json:"colors,omitempty"`
	// BucketLabels are the words of the facet's buckets, by bucket id.
	BucketLabels map[string]string `json:"bucket_labels,omitempty"`
}

// Display is the facet's own display keys.
func (f FacetSpec) Display() FacetDisplay {
	return FacetDisplay{Show: f.Show, ShowOnly: f.ShowOnly, ShowValues: f.ShowValues, LabelPrefix: f.LabelPrefix,
		ShowWhen: f.ShowWhen, ShowMax: f.ShowMax, ShowLabel: f.ShowLabel, RowClass: f.RowClass, Labels: f.Labels,
		Strip: f.Strip, Colors: f.Colors, BucketLabels: f.bucketLabels()}
}

// For is the facet as provider's rows show it: its display keys replaced by provider's on a merged facet,
// the labels provider leaves out kept.
func (f FacetSpec) For(provider string) FacetSpec {
	d, ok := f.Displays[provider]
	if !ok {
		return f
	}
	f.Show, f.ShowOnly, f.ShowValues, f.LabelPrefix, f.RowClass = d.Show, d.ShowOnly, d.ShowValues, d.LabelPrefix, d.RowClass
	f.ShowWhen, f.ShowMax, f.ShowLabel = d.ShowWhen, d.ShowMax, d.ShowLabel
	f.Strip, f.Colors = d.Strip, d.Colors
	if len(d.BucketLabels) > 0 {
		f.Buckets = slices.Clone(f.Buckets)
		for i, b := range f.Buckets {
			if l, ok := d.BucketLabels[b.ID]; ok {
				f.Buckets[i].Label = l
			}
		}
	}
	if len(d.Labels) > 0 {
		f.Labels = d.Labels
	}
	return f
}

func (f FacetSpec) bucketLabels() map[string]string {
	if len(f.Buckets) == 0 {
		return nil
	}
	out := make(map[string]string, len(f.Buckets))
	for _, b := range f.Buckets {
		out[b.ID] = b.Label
	}
	return out
}

// Sources are the providers the facet comes from, none for a facet of the profile.
func (f FacetSpec) Sources() []string {
	if len(f.Providers) > 0 {
		return f.Providers
	}
	if f.Provider != "" {
		return []string{f.Provider}
	}
	return nil
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
