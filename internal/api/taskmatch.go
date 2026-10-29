package api

import (
	"cmp"
	"encoding/json"
	"maps"
	"slices"
	"strings"
	"unicode"
)

// CountersTerm is the query parameter of CountersPath, repeated, that counts only the tasks holding every
// term, as TaskMatches reads them.
const CountersTerm = "term"

// accents folds the accents Portuguese and Spanish titles carry, without golang.org/x/text.
var accents = strings.NewReplacer(
	"á", "a", "à", "a", "â", "a", "ã", "a", "ä", "a",
	"é", "e", "è", "e", "ê", "e", "ë", "e",
	"í", "i", "ì", "i", "î", "i", "ï", "i",
	"ó", "o", "ò", "o", "ô", "o", "õ", "o", "ö", "o",
	"ú", "u", "ù", "u", "û", "u", "ü", "u",
	"ç", "c", "ñ", "n",
)

// Fold lowers s and drops its accents, a decomposed "é" (e and U+0301) included, the way the filters compare
// text.
func Fold(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.Is(unicode.Mn, r) {
			return -1
		}
		return r
	}, accents.Replace(strings.ToLower(s)))
}

// Texts are what a filter's free words search in a task: its title, notes, state, the text and the lists of
// text of its properties, its units, who owes it, its asks, its cards' people, its draft and its message. The
// cockpit's taskHaystack lists the same texts.
func (e TaskView) Texts() []string {
	out := []string{e.Title, e.Notes, e.State}
	for _, name := range slices.Sorted(maps.Keys(e.Props)) {
		var text string
		var list []string
		switch raw := e.Props[name]; {
		case json.Unmarshal(raw, &text) == nil:
			out = append(out, text)
		case json.Unmarshal(raw, &list) == nil:
			out = append(out, list...)
		}
	}
	for i, u := range e.Units {
		out = append(out, e.UnitLabel(i), string(u.Provider)+":"+u.Key)
	}
	if e.OwedBy != "" {
		out = append(out, e.OwedBy)
	}
	for _, a := range e.Asks {
		out = append(out, a.Summary, a.Asker)
	}
	for _, c := range e.Cards {
		if h := c.Card.LastHuman; h != nil {
			out = append(out, cmp.Or(h.Who.Name, h.Who.Key))
		}
		for _, p := range c.Card.WaitsOn {
			out = append(out, cmp.Or(p.Name, p.Key))
		}
	}
	if d, ok := e.DraftOf(); ok {
		out = append(out, d.Target, d.Where, d.Text, d.Reaction, d.Unit)
	}
	if m, _, ok := e.MessageOf(); ok {
		out = append(out, m.Text)
	}
	return out
}

// TaskMatches says whether every term is in one of e's Texts, ignoring case and accents.
func TaskMatches(e TaskView, terms []string) bool {
	if len(terms) == 0 {
		return true
	}
	texts := e.Texts()
	for i, t := range texts {
		texts[i] = Fold(t)
	}
	for _, t := range terms {
		t = Fold(t)
		if !slices.ContainsFunc(texts, func(s string) bool { return strings.Contains(s, t) }) {
			return false
		}
	}
	return true
}
