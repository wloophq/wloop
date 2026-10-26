package prrow

import (
	"slices"

	"github.com/wloophq/wloop/internal/api"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	"github.com/wloophq/wloop/internal/domain/unit"
	intentload "github.com/wloophq/wloop/internal/intent"
	"github.com/wloophq/wloop/internal/profile"
)

// FacetExtra prefixes the facet of an enum field of the brief intent's
// extra: extra.<name>.
const FacetExtra = "extra."

// extraDims are the enum extras of the pull requests' brief intent.
func extraDims(reg *dintent.Registry) []profile.DimDef {
	if reg == nil {
		return nil
	}
	var out []profile.DimDef
	for _, d := range profile.Vocabulary(reg) {
		if d.Extra != "" && slices.Contains(d.Providers, unit.GitHub) {
			out = append(out, d)
		}
	}
	return out
}

func registryOf(l *intentload.Loaded) *dintent.Registry {
	if l == nil {
		return nil
	}
	return l.Registry
}

// ExtraFacetSpecs are the facets of the brief intent's enum extras, their
// values in the schema's order.
func ExtraFacetSpecs(reg *dintent.Registry) []api.FacetSpec {
	var out []api.FacetSpec
	for _, d := range extraDims(reg) {
		out = append(out, api.FacetSpec{ID: FacetExtra + d.Extra, Label: d.Extra, Provider: string(unit.GitHub), Group: !d.List,
			Sort: "rank", Rank: d.Closed[unit.GitHub], Empty: "no brief", Menu: true})
	}
	return out
}

// extraValues are a row's values of each extra facet: the brief's values
// the schema declares.
func extraValues(reg *dintent.Registry, extra map[string]any) map[string][]string {
	out := map[string][]string{}
	for _, d := range extraDims(reg) {
		var vs []string
		switch v := extra[d.Extra].(type) {
		case string:
			vs = []string{v}
		case []string:
			vs = v
		case []any:
			for _, e := range v {
				if s, ok := e.(string); ok {
					vs = append(vs, s)
				}
			}
		}
		closed := d.Closed[unit.GitHub]
		vs = slices.DeleteFunc(slices.Clone(vs), func(v string) bool { return !slices.Contains(closed, v) })
		if len(vs) > 0 {
			out[FacetExtra+d.Extra] = vs
		}
	}
	return out
}
