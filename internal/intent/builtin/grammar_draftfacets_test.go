package builtin

import (
	"slices"
	"strings"
	"testing"
)

func TestDraftFacetsSayWhichFacetKeysADraftCardNeverReads(t *testing.T) {
	unread := []string{"show", "show_values", "show_when", "show_max", "show_label", "label_prefix", "strip", "colors", "merge", "units"}
	paths := map[string]Key{}
	for _, k := range draftFacetsGrammar {
		paths[k.Path] = k
	}
	for _, name := range unread {
		k, ok := paths["draft_facets.*."+name]
		if !ok || !strings.HasPrefix(k.Doc, "Not read") {
			t.Errorf("draft_facets.*.%s's doc %q, want it to start with Not read", name, k.Doc)
		}
	}
}

func TestDraftFacetsDeriveOnlyFromFacetsKeys(t *testing.T) {
	keys := []Key{{Path: "facets"}, {Path: "facets.*"}, {Path: "facetsx"}, {Path: "facets_style.*"}, {Path: "views.*.facets"}}
	var got []string
	for _, k := range draftFacetsOf(keys) {
		got = append(got, k.Path)
	}
	if want := []string{"draft_facets", "draft_facets.*"}; !slices.Equal(got, want) {
		t.Errorf("derived %q, want %q", got, want)
	}
}
