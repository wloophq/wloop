package catalog

import (
	"cmp"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/wloophq/wloop/internal/api"
	intentload "github.com/wloophq/wloop/internal/intent"
)

// TaskFacets are the profile's task facets as they are served: facets of
// every row, in the profile's order.
func TaskFacets(defs []intentload.FacetDef) []api.FacetSpec {
	out := make([]api.FacetSpec, 0, len(defs))
	for _, f := range defs {
		out = append(out, taskFacet(f))
	}
	return out
}

func taskFacet(f intentload.FacetDef) api.FacetSpec {
	spec := api.FacetSpec{ID: f.ID, Label: f.Label, Group: f.Group, Sort: f.Sort, Rank: slices.Clone(f.Rank), Labels: maps.Clone(f.Values),
		Empty: f.Empty, Match: f.Match, Menu: f.Menu, Strip: f.Strip}
	for _, b := range f.Buckets {
		within := ""
		if b.Within > 0 {
			within = withinText(b.Within)
		}
		spec.Buckets = append(spec.Buckets, api.Bucket{ID: b.ID, Label: b.Label, Within: within})
	}
	return spec
}

// withinText writes d as a Go duration the clients parse, in hours when whole.
func withinText(d time.Duration) string {
	if d%time.Hour == 0 {
		return fmt.Sprint(int64(d/time.Hour)) + "h"
	}
	return d.String()
}

// CheckFacets are the problems of facets of one id that do not group, sort,
// match and bucket alike: two plugins' facets, and a task facet against a
// plugin's. plugin are the facets the plugins serve, those of one id that
// agree already merged into one.
func CheckFacets(task []intentload.FacetDef, plugin []api.FacetSpec) intentload.Problems {
	var out intentload.Problems
	for i, a := range plugin {
		for _, b := range plugin[i+1:] {
			if diff := a.MechanicsDiff(b); a.ID == b.ID && diff != "" {
				out = append(out, intentload.Problem{File: "plugins/" + b.Provider + "/plugin.toml", Field: "source.ui.facets." + b.ID,
					Msg: fmt.Sprintf("the %s plugin's facet %s differs from the %s plugin's: %s", b.Provider, b.ID, a.Provider, diff)})
			}
		}
	}
	for _, f := range task {
		mine := taskFacet(f)
		for _, p := range plugin {
			if diff := p.MechanicsDiff(mine); p.ID == f.ID && diff != "" {
				out = append(out, f.Problem(diffKey(diff), "facets.%s differs from the %s plugin's facet %s: %s", f.ID, cmp.Or(p.Provider, "plugins'"), p.ID, diff))
			}
		}
	}
	return out
}

func diffKey(diff string) string {
	for _, k := range []string{"group", "sort", "match", "buckets"} {
		if strings.HasPrefix(diff, k) {
			return k
		}
	}
	return "fact"
}
