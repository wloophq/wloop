package catalog

import (
	"encoding/json"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/wloophq/wloop/internal/domain/workflow"
	intentload "github.com/wloophq/wloop/internal/intent"
)

// FactFacets are the values of the facets defs declares that f holds the fact of, as text: a true bool is
// "yes", a number its decimal, an instant RFC 3339, a list its elements; false, zero, empty and the zero
// instant are no value.
func FactFacets(defs []intentload.FacetDef, f workflow.Facts) map[string][]string {
	out := map[string][]string{}
	for _, d := range defs {
		if v, ok := f[d.Fact]; ok {
			if vs := texts(v); len(vs) > 0 {
				out[d.ID] = vs
			}
		}
	}
	return out
}

// RecordFacets are the values of the facets defs declares over the fields of a record of kind, the facets
// whose fact is <kind>.<field>, read from the record's data as FactFacets reads a fact.
func RecordFacets(defs []intentload.FacetDef, kind string, data json.RawMessage) map[string][]string {
	out := map[string][]string{}
	var fields map[string]any
	if json.Unmarshal(data, &fields) != nil {
		return out
	}
	for _, d := range defs {
		path, ok := strings.CutPrefix(d.Fact, kind+".")
		if !ok {
			continue
		}
		if vs := texts(field(fields, path)); len(vs) > 0 {
			out[d.ID] = vs
		}
	}
	return out
}

func field(fields map[string]any, path string) any {
	var v any = fields
	for name := range strings.SplitSeq(path, ".") {
		m, ok := v.(map[string]any)
		if !ok {
			return nil
		}
		v = m[name]
	}
	return v
}

func texts(v any) []string {
	switch x := v.(type) {
	case []string:
		return appendNew(nil, x...)
	case []any:
		var out []string
		for _, e := range x {
			out = appendNew(out, texts(e)...)
		}
		return out
	}
	if s := text(v); s != "" {
		return []string{s}
	}
	return nil
}

func text(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case bool:
		if x {
			return "yes"
		}
	case int:
		if x != 0 {
			return strconv.Itoa(x)
		}
	case int64:
		if x != 0 {
			return strconv.FormatInt(x, 10)
		}
	case float64:
		if x != 0 {
			return strconv.FormatFloat(x, 'f', -1, 64)
		}
	case time.Duration:
		if x != 0 {
			return x.String()
		}
	case time.Time:
		if !x.IsZero() {
			return x.Format(time.RFC3339)
		}
	}
	return ""
}

// MergeFacets joins a task's own facet values with those of its units' rows, rows in unit order, the
// primary unit's first and nil for a unit without a row. A facet defs declares as rows-first takes the rows'
// values, its own only when no row has one; any other joins its own and then the rows', each value once. A
// facet declared for the primary unit reads the first row alone. A facet defs does not declare comes from
// the rows alone.
func MergeFacets(defs []intentload.FacetDef, own map[string][]string, rows []map[string][]string) map[string][]string {
	out := map[string][]string{}
	declared := make(map[string]bool, len(defs))
	for _, d := range defs {
		declared[d.ID] = true
		from := rows
		if d.Units == "primary" {
			from = rows[:min(1, len(rows))]
		}
		var theirs []string
		for _, r := range from {
			theirs = appendNew(theirs, r[d.ID]...)
		}
		vs := theirs
		if d.Merge != "rows-first" || len(theirs) == 0 {
			vs = appendNew(appendNew(nil, own[d.ID]...), theirs...)
		}
		if len(vs) > 0 {
			out[d.ID] = vs
		}
	}
	for _, r := range rows {
		for id, vs := range r {
			if !declared[id] {
				if merged := appendNew(out[id], vs...); len(merged) > 0 {
					out[id] = merged
				}
			}
		}
	}
	return out
}

// appendNew appends to out each of vs it does not hold yet, empty ones left out.
func appendNew(out []string, vs ...string) []string {
	for _, v := range vs {
		if v != "" && !slices.Contains(out, v) {
			out = append(out, v)
		}
	}
	return out
}
