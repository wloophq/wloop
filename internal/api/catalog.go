package api

import (
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"time"
)

// Bucket is the id of the first bucket whose Within is above now - t, the
// one with Within "" catching the rest; "" when no bucket takes t.
func BucketOf(buckets []Bucket, now, t time.Time) string {
	delta := now.Sub(t)
	for _, b := range buckets {
		if b.Within == "" {
			return b.ID
		}
		if d, err := time.ParseDuration(b.Within); err == nil && delta < d {
			return b.ID
		}
	}
	return ""
}

// VersionOf is the first 12 hex digits of the SHA-256 of v as JSON: the version of a served catalog, whose own Version is empty.
func VersionOf(v any) string {
	raw, _ := json.Marshal(v)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:6])
}

// MechanicsDiff says how o groups, sorts, matches or buckets differently from
// f, "" when it does not: the mechanics two declarations of one facet id must share.
func (f FacetSpec) MechanicsDiff(o FacetSpec) string {
	switch {
	case f.Group != o.Group:
		return fmt.Sprintf("group is %t, not %t", f.Group, o.Group)
	case f.Sort != o.Sort:
		return fmt.Sprintf("sort is %q, not %q", f.Sort, o.Sort)
	case cmp.Or(f.Match, "exact") != cmp.Or(o.Match, "exact"):
		return fmt.Sprintf("match is %q, not %q", cmp.Or(f.Match, "exact"), cmp.Or(o.Match, "exact"))
	case !slices.EqualFunc(f.Buckets, o.Buckets, func(x, y Bucket) bool { return x.ID == y.ID && x.Within == y.Within }):
		return "buckets differ"
	}
	return ""
}

// Situation is the spec of a provider's kind; ok is false for an unknown
// one.
func (c CatalogResponse) Situation(provider string, k Kind) (SituationSpec, bool) {
	i := slices.IndexFunc(c.Situations, func(s SituationSpec) bool { return s.Provider == provider && s.Kind == k })
	if i < 0 {
		return SituationSpec{}, false
	}
	return c.Situations[i], true
}

// KindLabel is the label of a row's kind, the kind itself when the catalog
// has none.
func (c CatalogResponse) KindLabel(r Row) string {
	if s, ok := c.Situation(string(r.Unit.Provider), r.Kind); ok {
		return s.Label
	}
	return string(r.Kind)
}

// ViewsOn are the views a surface shows, in order.
func (c CatalogResponse) ViewsOn(surface string) []ViewSpec {
	var out []ViewSpec
	for _, v := range c.Views {
		if slices.Contains(v.Surfaces, surface) {
			out = append(out, v)
		}
	}
	slices.SortStableFunc(out, func(a, b ViewSpec) int { return a.Order - b.Order })
	return out
}
