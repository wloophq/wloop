package api

import (
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
