package lens_test

import (
	"slices"
	"testing"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/api/lens"
)

func ids(specs []api.FacetSpec) []string {
	var out []string
	for _, s := range specs {
		out = append(out, s.ID)
	}
	return out
}

func TestMergeKeepsFirstPositionAndLastSpec(t *testing.T) {
	plugin := []api.FacetSpec{{ID: "repo", Label: "Repo"}, {ID: "author", Label: "Author"}}
	profile := []api.FacetSpec{{ID: "state", Label: "State"}, {ID: "author", Label: "Asked by"}}
	got := lens.Merge(plugin, profile)
	if want := []string{"repo", "author", "state"}; !slices.Equal(ids(got), want) {
		t.Fatalf("ids %v, want %v", ids(got), want)
	}
	if got[1].Label != "Asked by" {
		t.Fatalf("the later spec does not win: %+v", got[1])
	}
}

func TestOfferedKeepsTheFieldsSomeItemHasAndAddsTagKeys(t *testing.T) {
	specs := []api.FacetSpec{{ID: "repo"}, {ID: "author"}, {ID: "tag", Label: "Tag", Match: "prefix"}}
	items := []lens.Item{
		{Facets: map[string][]string{"repo": {"acme/api"}, "author": {""}, "tag": {"area:billing", "plain"}}},
		{Facets: map[string][]string{"repo": {"acme/web"}, "tag": {"kind:bug", "Bad Key:x"}}},
	}
	got := lens.Offered(items, specs, api.LensWords{TagKey: "{{.label}} {{.key}}", TagKeyEmpty: "no {{.key}}"})
	if want := []string{"repo", "tag", "tag:area", "tag:kind"}; !slices.Equal(ids(got), want) {
		t.Fatalf("ids %v, want %v", ids(got), want)
	}
	if got[2].Label != "Tag area" || got[2].Empty != "no area" || !got[2].Group {
		t.Fatalf("tag field %+v", got[2])
	}
}
