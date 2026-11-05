package catalog

import (
	"cmp"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/domain/task"
	intentload "github.com/wloophq/wloop/internal/intent"
)

// TaskFacets are the profile's task facets as they are served: facets of
// every row, in the profile's order. A facet that declares no rank takes the
// order of the values its fact has in tasks (the states, the categories their
// states declare, an enum property's values) or in contexts, the configured
// contexts, and the labels of the states.
func TaskFacets(defs []intentload.FacetDef, tasks intentload.TasksDef, contexts []string) []api.FacetSpec {
	out := make([]api.FacetSpec, 0, len(defs))
	for _, f := range defs {
		spec := taskFacet(f)
		if len(spec.Rank) == 0 {
			deriveOrder(&spec, f, tasks, contexts)
		}
		out = append(out, spec)
	}
	return out
}

// deriveOrder gives spec the values of the fact its facet reads, in the order the profile declares them.
func deriveOrder(spec *api.FacetSpec, f intentload.FacetDef, tasks intentload.TasksDef, contexts []string) {
	labels := map[string]string{}
	var rank []string
	switch name, _ := strings.CutPrefix(f.Fact, "task."); {
	case f.Fact == "task.state":
		for _, st := range tasks.States {
			rank = append(rank, st.Name)
			labels[st.Name] = cmp.Or(st.Label, st.Name)
		}
	case f.Fact == "task.category":
		for _, st := range tasks.States {
			if c := string(st.Category); !slices.Contains(rank, c) {
				rank = append(rank, c)
			}
		}
	case strings.HasPrefix(name, "prop.") && f.Fact != name:
		p, ok := tasks.Prop(strings.TrimPrefix(name, "prop."))
		switch {
		case !ok:
		case p.Role == task.RoleContexts:
			rank = contexts
		case len(p.Values) > 0:
			rank = p.Values
		}
	}
	if len(rank) == 0 {
		return
	}
	spec.Rank = make([]string, len(rank))
	for i, v := range rank {
		spec.Rank[i] = api.Fold(v)
		if _, ok := spec.Labels[v]; ok {
			continue
		}
		if label := labels[v]; label != "" && label != v {
			if spec.Labels == nil {
				spec.Labels = map[string]string{}
			}
			spec.Labels[v] = label
		}
	}
}

func taskFacet(f intentload.FacetDef) api.FacetSpec {
	spec := api.FacetSpec{ID: f.ID, Label: f.Label, Fact: f.Fact, Group: f.Group, Sort: f.Sort, Rank: slices.Clone(f.Rank), Labels: maps.Clone(f.Values),
		Empty: f.Empty, Match: f.Match, Menu: f.Menu, Strip: f.Strip,
		Show: f.Show.Part, ShowOnly: slices.Clone(f.Show.Only), ShowValues: f.Show.Values, LabelPrefix: f.LabelPrefix}
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

// CheckFacets are the problems of facets of one id that do not group, sort, rank,
// match and bucket alike: two plugins' facets, and a task facet against a
// plugin's. plugin are the facets the plugins serve, those of one id that
// agree already merged into one.
func CheckFacets(task []intentload.FacetDef, plugin []api.FacetSpec) intentload.Problems {
	var out intentload.Problems
	for i, a := range plugin {
		for _, b := range plugin[i+1:] {
			if diff := a.MechanicsDiff(b); a.ID == b.ID && diff != "" {
				out = append(out, intentload.Problem{File: "plugins/" + strings.Join(b.Sources(), "+") + "/plugin.toml", Field: "source.ui.facets." + b.ID,
					Msg: fmt.Sprintf("the facet %s of %s differs from that of %s: %s", b.ID, sources(b), sources(a), diff)})
			}
		}
	}
	for _, f := range task {
		mine := taskFacet(f)
		for _, p := range plugin {
			if diff := p.MechanicsDiff(mine); p.ID == f.ID && diff != "" {
				out = append(out, f.Problem(diffKey(diff), "facets.%s differs from the facet %s of %s: %s", f.ID, p.ID, sources(p), diff))
			}
		}
	}
	return out
}

func sources(f api.FacetSpec) string { return "plugin " + strings.Join(f.Sources(), "+") }

func diffKey(diff string) string {
	for _, k := range []string{"group", "sort", "rank", "match", "buckets"} {
		if strings.HasPrefix(diff, k) {
			return k
		}
	}
	return "fact"
}

// LensWords are the profile's lens words as clients read them.
func LensWords(w intentload.LensWords) api.LensWords {
	return api.LensWords{DefaultGroup: w.DefaultGroup, BoardGroup: w.BoardGroup, NoGroup: w.NoGroup, Copied: w.Copied,
		CopiedGrouped: w.CopiedGrouped, TagKey: w.TagKey, TagKeyEmpty: w.TagKeyEmpty, Grouped: w.Grouped, Sorted: w.Sorted,
		Descending: w.Descending, NoLens: w.NoLens, RowWords: api.RowWords(w.Row)}
}
