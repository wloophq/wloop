package tui

import (
	"maps"
	"slices"
	"strings"
	"unicode"

	"github.com/wloophq/wloop/internal/api"
)

// accents folds the accents Portuguese and Spanish titles carry, without
// golang.org/x/text.
var accents = strings.NewReplacer(
	"á", "a", "à", "a", "â", "a", "ã", "a", "ä", "a",
	"é", "e", "è", "e", "ê", "e", "ë", "e",
	"í", "i", "ì", "i", "î", "i", "ï", "i",
	"ó", "o", "ò", "o", "ô", "o", "õ", "o", "ö", "o",
	"ú", "u", "ù", "u", "û", "u", "ü", "u",
	"ç", "c", "ñ", "n",
)

// fold also drops combining marks, so a decomposed "é" (e and U+0301)
// matches as "e".
func fold(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.Is(unicode.Mn, r) {
			return -1
		}
		return r
	}, accents.Replace(strings.ToLower(s)))
}

// Matches reports whether every word of query is in one of the row's
// generic fields, its kind's label, its facet values or extra, ignoring
// case and accents.
func Matches(r api.Row, query, kindLabel string, extra ...string) bool {
	words := strings.Fields(fold(query))
	if len(words) == 0 {
		return true
	}
	fields := []string{r.Title, r.Label, r.Link, string(r.Kind), kindLabel, r.Summary.WaitsOn, r.Summary.Reason, r.Summary.Line}
	for _, id := range slices.Sorted(maps.Keys(r.Facets)) {
		fields = append(fields, r.Facets[id]...)
	}
	fields = append(fields, extra...)
	for i, f := range fields {
		fields[i] = fold(f)
	}
	for _, w := range words {
		if !slices.ContainsFunc(fields, func(f string) bool { return strings.Contains(f, w) }) {
			return false
		}
	}
	return true
}
