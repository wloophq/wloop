package lens

import (
	"cmp"
	"fmt"
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
	// Template is an age piece's show_label, whose {{.at}} a client fills with Raw in its own locale.
	Template string `json:"template,omitempty"`
}

// AtVar is the variable of an age piece's template a client fills with its time.
const AtVar = "{{.at}}"

// Parts are the pieces of it each part of a row shows, by part, from the specs that show in one, in their
// order: a spec of another provider places none, and a spec merged across providers places it with its
// provider's display keys, and a spec whose show_when the item does not hold places none. A value "none" and
// a time facet's value that is no time never show, the age part holds the first time of its first facet,
// labeled with its age at now in words (or its show_label with {{.age}} filled), and a part with show_max
// counts the values past it into one last piece.
func Parts(it Item, specs []api.FacetSpec, words api.LensWords, now time.Time) map[string][]Piece {
	out := map[string][]Piece{}
	for _, s := range specs {
		if s.Provider != "" && it.Provider != "" && s.Provider != it.Provider {
			continue
		}
		spec := s.For(it.Provider)
		if spec.Show == "" || (spec.Show == "age" && len(out["age"]) > 0) || !holds(it, spec.ShowWhen) {
			continue
		}
		var pieces []Piece
		rest := 0
		for _, raw := range it.Facets[spec.ID] {
			p, ok := piece(spec, raw, words, now)
			if !ok || (len(spec.ShowOnly) > 0 && !slices.ContainsFunc(spec.ShowOnly, func(v string) bool { return api.Fold(v) == api.Fold(raw) })) {
				continue
			}
			if spec.ShowMax > 0 && len(pieces) == spec.ShowMax {
				rest++
				continue
			}
			pieces = append(pieces, p)
			if spec.ShowValues == "first" || spec.Show == "age" {
				break
			}
		}
		if rest > 0 {
			pieces = append(pieces, Piece{Field: spec.ID, Label: Word(words.More, map[string]string{"n": strconv.Itoa(rest)})})
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

// holds says whether it holds, in each facet when names, one of its values.
func holds(it Item, when map[string][]string) bool {
	for facet, values := range when {
		if !slices.ContainsFunc(it.Facets[facet], func(v string) bool {
			return slices.ContainsFunc(values, func(w string) bool { return api.Fold(v) == api.Fold(w) })
		}) {
			return false
		}
	}
	return true
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
			if spec.ShowLabel != "" {
				p.Label, p.Template = strings.ReplaceAll(spec.ShowLabel, "{{.age}}", p.Label), spec.ShowLabel
			}
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

// Countdown is the time left before a pending round sends, in the row words; it mirrors app.js's left.
func Countdown(w api.RowWords, left time.Duration) string {
	s := int((left + time.Second - 1) / time.Second)
	switch {
	case s <= 0:
		return w.CountdownSending
	case s <= 99:
		return Word(w.CountdownSeconds, map[string]string{"s": strconv.Itoa(s)})
	}
	return Word(w.CountdownMinutes, map[string]string{"m": strconv.Itoa(s / 60), "s": fmt.Sprintf("%02d", s%60)})
}
