package lens

import (
	"cmp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/wloophq/wloop/internal/api"
)

// Piece is one value a row shows of a facet: Field and Value are the qualifier a click on it toggles (a
// time facet's Value its bucket), Raw the item's own value and Label what the row reads.
type Piece struct {
	Field string `json:"field"`
	Value string `json:"value"`
	Raw   string `json:"raw"`
	Label string `json:"label"`
}

// Parts are the pieces of it each part of a row shows, by part, from the specs that show in one, in their
// order: a spec of another provider places none, and a spec merged across providers places it with its
// provider's display keys. A value "none" and a
// time facet's value that is no time never show, and the age part holds the first time of its first facet,
// labeled with its age at now in words.
func Parts(it Item, specs []api.FacetSpec, words api.LensWords, now time.Time) map[string][]Piece {
	out := map[string][]Piece{}
	for _, s := range specs {
		if s.Provider != "" && it.Provider != "" && s.Provider != it.Provider {
			continue
		}
		spec := s.For(it.Provider)
		if spec.Show == "" || (spec.Show == "age" && len(out["age"]) > 0) {
			continue
		}
		var pieces []Piece
		for _, raw := range it.Facets[spec.ID] {
			p, ok := piece(spec, raw, words, now)
			if !ok || (len(spec.ShowOnly) > 0 && !slices.ContainsFunc(spec.ShowOnly, func(v string) bool { return api.Fold(v) == api.Fold(raw) })) {
				continue
			}
			pieces = append(pieces, p)
			if spec.ShowValues == "first" || spec.Show == "age" {
				break
			}
		}
		out[spec.Show] = append(out[spec.Show], pieces...)
	}
	for part, ps := range out {
		if len(ps) == 0 {
			delete(out, part)
		}
	}
	return out
}

func piece(spec api.FacetSpec, raw string, words api.LensWords, now time.Time) (Piece, bool) {
	if raw == "" || api.Fold(raw) == none {
		return Piece{}, false
	}
	p := Piece{Field: spec.ID, Value: raw, Raw: raw, Label: cmp.Or(spec.Labels[raw], raw)}
	if spec.Sort == "time" || len(spec.Buckets) > 0 {
		at, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			return Piece{}, false
		}
		if len(spec.Buckets) > 0 {
			if p.Value = api.BucketOf(spec.Buckets, now, at); p.Value == "" {
				return Piece{}, false
			}
			if i := slices.IndexFunc(spec.Buckets, func(b api.Bucket) bool { return b.ID == p.Value }); i >= 0 {
				p.Label = cmp.Or(spec.Buckets[i].Label, p.Value)
			}
		}
		if spec.Show == "age" {
			p.Label = Age(words.RowWords, now.Sub(at))
		}
	}
	if spec.LabelPrefix != "" && !strings.HasPrefix(p.Label, spec.LabelPrefix) {
		p.Label = spec.LabelPrefix + p.Label
	}
	return p, true
}

// Age is d in the row words: under a minute, in minutes, in hours and minutes, or in days and hours; a
// negative d reads as under a minute.
func Age(w api.RowWords, d time.Duration) string {
	mins := int(max(d, 0) / time.Minute)
	n := func(v int) string { return strconv.Itoa(v) }
	switch {
	case mins < 1:
		return w.AgeNow
	case mins < 60:
		return Word(w.AgeMinutes, map[string]string{"m": n(mins)})
	case mins < 24*60:
		return Word(w.AgeHours, map[string]string{"h": n(mins / 60), "m": n(mins % 60)})
	}
	return Word(w.AgeDays, map[string]string{"d": n(mins / (24 * 60)), "h": n(mins / 60 % 24)})
}
