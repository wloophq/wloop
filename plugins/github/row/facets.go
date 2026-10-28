package row

import (
	"slices"
	"strings"
	"time"
)

// The facets of a row whose values the plugin reports; the host derives
// the facets that read its own state or the row's situation after its
// overlays.
const (
	FacetRepo   = "repo"
	FacetAuthor = "author"
	FacetStatus = "status"
	FacetCI     = "ci"
	FacetScope  = "scope"
	FacetOpened = "opened"
)

// facets are the row's values of the plugin's facets; a time facet holds
// RFC 3339 instants the client buckets.
func (r Row) facets() map[string][]string {
	p := r.Payload
	author := []string{p.Author}
	if MineAuthor(p) {
		author = []string{"me", p.Author}
	}
	ci := []string{or(strings.ToLower(p.Facts.Checks.State), "none")}
	out := map[string][]string{
		FacetRepo:   {p.Repo},
		FacetAuthor: author,
		FacetStatus: {or(p.Workspace.Status, "none")},
		FacetCI:     append(ci, p.Facts.Checks.Failing...),
		FacetScope:  slices.Clone(p.Reasons),
	}
	if !p.Facts.CreatedAt.IsZero() {
		out[FacetOpened] = []string{Stamp(p.Facts.CreatedAt)}
	}
	for id, vs := range out {
		vs = slices.DeleteFunc(vs, func(v string) bool { return v == "" })
		if len(vs) == 0 {
			delete(out, id)
			continue
		}
		out[id] = vs
	}
	return out
}

// Stamp is t as a time facet holds it.
func Stamp(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

func or(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}
