// Package lens is the one semantics of a view's filter, group and sort over facets. The cockpit's app.js
// follows it through the shared cases in testdata/cases.json.
package lens

import (
	"cmp"
	"maps"
	"math"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/wloophq/wloop/internal/api"
)

const none = "none"

// Item is what the lens reads of one thing on a list: its facet values and the texts a free word searches.
type Item struct {
	Facets map[string][]string `json:"facets"`
	Texts  []string            `json:"texts"`
}

// Qualifier is a field:value term of a query. Value is folded.
type Qualifier struct {
	Field, Value string
	Neg          bool
}

// Query is a parsed filter: free words, each of which every item must hold in some text, and qualifiers,
// whose values of one field are alternatives and whose fields all must hold. Terms are folded.
type Query struct {
	Terms []string
	Quals []Qualifier
}

var (
	tokenRE     = regexp.MustCompile(`[^\s\x0B\p{Z}\x{FEFF}"]*"[^"]*"?|[^\s\x0B\p{Z}\x{FEFF}"]+`)
	qualifierRE = regexp.MustCompile(`^(-?)([a-zA-Z][a-zA-Z.-]*):([^\n\r\x{2028}\x{2029}]+)$`)
	tagKeyRE    = regexp.MustCompile(`^([a-z-]+)$`)
)

func unquote(s string) string {
	s = strings.TrimPrefix(s, `"`)
	return strings.TrimSuffix(s, `"`)
}

// Parse reads q as space-separated tokens, a quoted value kept whole. A token is a qualifier when it reads
// [-]field:value for a field of specs and has a value; any other token, an unknown field's included, is a
// free word.
func Parse(q string, specs map[string]api.FacetSpec) Query {
	var out Query
	for _, tok := range tokenRE.FindAllString(q, -1) {
		if m := qualifierRE.FindStringSubmatch(tok); m != nil {
			field := strings.ToLower(m[2])
			value := api.Fold(unquote(m[3]))
			if value == "@me" {
				value = "me"
			}
			if _, ok := specs[field]; ok && value != "" {
				out.Quals = append(out.Quals, Qualifier{Field: field, Value: value, Neg: m[1] == "-"})
				continue
			}
		}
		if term := api.Fold(unquote(tok)); term != "" {
			out.Terms = append(out.Terms, term)
		}
	}
	return out
}

// Match says whether it holds every term and every field's qualifiers.
func (q Query) Match(it Item, specs map[string]api.FacetSpec, now time.Time) bool {
	if len(q.Terms) > 0 {
		texts := make([]string, len(it.Texts))
		for i, t := range it.Texts {
			texts[i] = api.Fold(t)
		}
		for _, term := range q.Terms {
			if !slices.ContainsFunc(texts, func(t string) bool { return strings.Contains(t, term) }) {
				return false
			}
		}
	}
	type filter struct{ inc, exc []string }
	filters := map[string]*filter{}
	for _, x := range q.Quals {
		f := filters[x.Field]
		if f == nil {
			f = &filter{}
			filters[x.Field] = f
		}
		if x.Neg {
			f.exc = append(f.exc, x.Value)
		} else {
			f.inc = append(f.inc, x.Value)
		}
	}
	for field, f := range filters {
		spec := specs[field]
		values := Values(it, spec, now)
		hit := func(v string) bool {
			return slices.ContainsFunc(values, func(x string) bool { return matches(spec.Match, x, v) })
		}
		if (len(f.inc) > 0 && !slices.ContainsFunc(f.inc, hit)) || slices.ContainsFunc(f.exc, hit) {
			return false
		}
	}
	return true
}

func matches(kind, v, q string) bool {
	switch kind {
	case "suffix":
		return v == q || strings.HasSuffix(v, "/"+q)
	case "prefix":
		return v == q || strings.HasPrefix(v, q+":")
	case "team":
		return v == q || v == "team:"+q
	}
	return v == q
}

// Values are the item's values of spec's facet, folded the way filters compare: a time facet's are its
// buckets at now, "none" without a time.
func Values(it Item, spec api.FacetSpec, now time.Time) []string {
	raw := rawValues(it, spec, now)
	out := make([]string, len(raw))
	for i, v := range raw {
		out[i] = api.Fold(v)
	}
	return out
}

func rawValues(it Item, spec api.FacetSpec, now time.Time) []string {
	var vs []string
	for _, v := range it.Facets[spec.ID] {
		if v != "" {
			vs = append(vs, v)
		}
	}
	if len(spec.Buckets) == 0 {
		return vs
	}
	var out []string
	for _, v := range vs {
		if b := bucketOf(spec.Buckets, v, now); b != "" {
			out = append(out, b)
		}
	}
	if len(out) == 0 {
		return []string{none}
	}
	return out
}

func bucketOf(buckets []api.Bucket, value string, now time.Time) string {
	at, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return ""
	}
	return api.BucketOf(buckets, now, at)
}

// Group is the items of one value of a facet; Items index the slice GroupBy read.
type Group struct {
	Key, Label string
	Items      []int
}

// GroupBy puts each item under its first value of spec, under "none" without one, or under each of its
// values when the facet matches by prefix. Groups come in rank order, then by name, "none" last.
func GroupBy(items []Item, spec api.FacetSpec, now time.Time) []Group {
	return groupWith(items, spec, func(it Item) []string {
		keys := rawValues(it, spec, now)
		if spec.Match != "prefix" && len(keys) > 1 {
			keys = keys[:1]
		}
		return keys
	})
}

// GroupField resolves a group field id: a facet's own, or "<facet>:<key>" for a prefix-match facet, which
// groups by the values starting with "<key>:", an item under each of them. For the second kind it returns the
// keys of an item, to give GroupByKeys; for the first, nil keys and GroupBy applies.
func GroupField(id string, specs map[string]api.FacetSpec) (api.FacetSpec, func(Item) []string, bool) {
	if spec, ok := specs[id]; ok {
		return spec, nil, true
	}
	facet, key, found := strings.Cut(id, ":")
	spec, ok := specs[facet]
	if !found || !ok || spec.Match != "prefix" || !tagKeyRE.MatchString(key) {
		return api.FacetSpec{}, nil, false
	}
	keys := func(it Item) []string {
		var out []string
		for _, v := range it.Facets[facet] {
			if strings.HasPrefix(v, key+":") {
				out = append(out, v)
			}
		}
		return out
	}
	return api.FacetSpec{ID: id, Label: spec.Label + " " + key, Group: true, Menu: true, Empty: "no " + key}, keys, true
}

// GroupByKeys groups like GroupBy, an item under each of the keys the function gives, "none" without one.
func GroupByKeys(items []Item, spec api.FacetSpec, keys func(Item) []string) []Group {
	return groupWith(items, spec, keys)
}

func groupWith(items []Item, spec api.FacetSpec, keysOf func(Item) []string) []Group {
	byKey := map[string]*Group{}
	var order []*Group
	for i, it := range items {
		keys := keysOf(it)
		if len(keys) == 0 {
			keys = []string{none}
		}
		seen := map[string]bool{}
		for _, raw := range keys {
			key := api.Fold(raw)
			if seen[key] {
				continue
			}
			seen[key] = true
			g := byKey[key]
			if g == nil {
				g = &Group{Key: key, Label: label(spec, raw)}
				byKey[key] = g
				order = append(order, g)
			}
			g.Items = append(g.Items, i)
		}
	}
	slices.SortStableFunc(order, func(a, b *Group) int { return compareKeys(spec, a.Key, b.Key, false) })
	out := make([]Group, len(order))
	for i, g := range order {
		out[i] = *g
	}
	return out
}

func label(spec api.FacetSpec, raw string) string {
	if raw == "" || raw == none {
		return cmp.Or(spec.Empty, none)
	}
	return cmp.Or(spec.Labels[raw], raw)
}

// SortBy orders the items' indexes by spec: a time facet by its first time, any other by key, ranked values
// first in rank order. An item without a value is always last in a key order and first in an ascending time
// order. Ties keep the input order.
func SortBy(items []Item, spec api.FacetSpec, desc bool, now time.Time) []int {
	idx := make([]int, len(items))
	for i := range idx {
		idx[i] = i
	}
	slices.SortStableFunc(idx, func(a, b int) int {
		if spec.Sort == "time" {
			c := cmp.Compare(firstTime(items[a], spec), firstTime(items[b], spec))
			if desc {
				c = -c
			}
			return c
		}
		return compareKeys(spec, firstKey(items[a], spec, now), firstKey(items[b], spec, now), desc)
	})
	return idx
}

func firstTime(it Item, spec api.FacetSpec) int64 {
	for _, v := range it.Facets[spec.ID] {
		if v == "" {
			continue
		}
		if at, err := time.Parse(time.RFC3339, v); err == nil {
			return at.UnixMilli()
		}
		break
	}
	return math.MinInt64
}

func firstKey(it Item, spec api.FacetSpec, now time.Time) string {
	if vs := rawValues(it, spec, now); len(vs) > 0 {
		return api.Fold(vs[0])
	}
	return none
}

type keyRank struct {
	band int
	idx  int
	name string
}

func rankOf(spec api.FacetSpec, key string) keyRank {
	if key == none {
		return keyRank{band: 2}
	}
	if i := slices.Index(spec.Rank, key); i >= 0 {
		return keyRank{band: 0, idx: i}
	}
	return keyRank{band: 1, name: key}
}

func compareKeys(spec api.FacetSpec, a, b string, desc bool) int {
	ra, rb := rankOf(spec, a), rankOf(spec, b)
	if ra.band != rb.band {
		if ra.band == 2 || rb.band == 2 || !desc {
			return ra.band - rb.band
		}
		return rb.band - ra.band
	}
	c := cmp.Or(cmp.Compare(ra.idx, rb.idx), cmp.Compare(ra.name, rb.name))
	if desc {
		return -c
	}
	return c
}

// Merge lists the specs of lists once per id, at the position of the first and with the last's spec.
func Merge(lists ...[]api.FacetSpec) []api.FacetSpec {
	var out []api.FacetSpec
	at := map[string]int{}
	for _, l := range lists {
		for _, s := range l {
			if i, ok := at[s.ID]; ok {
				out[i] = s
				continue
			}
			at[s.ID] = len(out)
			out = append(out, s)
		}
	}
	return out
}

// ByID indexes specs by id.
func ByID(specs []api.FacetSpec) map[string]api.FacetSpec {
	out := make(map[string]api.FacetSpec, len(specs))
	for _, s := range specs {
		out[s.ID] = s
	}
	return out
}

// Offered are the fields a list's menus offer: the specs some item has a value of, in order, then a
// "tag:<key>" field for each key of the prefix-match "tag" facet's values the items hold.
func Offered(items []Item, specs []api.FacetSpec) []api.FacetSpec {
	has := map[string]bool{}
	tagKeys := map[string]bool{}
	for _, it := range items {
		for id, vs := range it.Facets {
			if slices.ContainsFunc(vs, func(v string) bool { return v != "" }) {
				has[id] = true
			}
		}
		for _, v := range it.Facets["tag"] {
			if key, _, ok := strings.Cut(v, ":"); ok && tagKeyRE.MatchString(key) {
				tagKeys[key] = true
			}
		}
	}
	var out []api.FacetSpec
	for _, s := range specs {
		if has[s.ID] {
			out = append(out, s)
		}
	}
	byID := ByID(specs)
	for _, key := range slices.Sorted(maps.Keys(tagKeys)) {
		if spec, _, ok := GroupField("tag:"+key, byID); ok {
			out = append(out, spec)
		}
	}
	return out
}
