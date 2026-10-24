package tui

import (
	"maps"
	"slices"
	"strings"

	"github.com/wloophq/wloop/internal/api"
)

// Matches reports whether every word of query is in one of the row's
// generic fields, its kind's label, its facet values or extra, ignoring
// case and accents.
func Matches(r api.Row, query, kindLabel string, extra ...string) bool {
	words := strings.Fields(api.Fold(query))
	if len(words) == 0 {
		return true
	}
	fields := []string{r.Title, r.Label, r.Link, string(r.Kind), kindLabel, r.Summary.WaitsOn, r.Summary.Reason, r.Summary.Line}
	for _, id := range slices.Sorted(maps.Keys(r.Facets)) {
		fields = append(fields, r.Facets[id]...)
	}
	fields = append(fields, extra...)
	for i, f := range fields {
		fields[i] = api.Fold(f)
	}
	for _, w := range words {
		if !slices.ContainsFunc(fields, func(f string) bool { return strings.Contains(f, w) }) {
			return false
		}
	}
	return true
}
