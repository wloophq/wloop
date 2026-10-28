package api

import (
	"cmp"
	"slices"
	"strings"
	"unicode"
)

// CountersTerm is the query parameter of CountersPath, repeated, that counts only the entries holding every
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

// Texts are what a filter's free words search in an entry: its title, notes, state, kind, area, project and
// contexts, its units, the people it waits on, its items, its cards' people, its draft and its message. The
// cockpit's entryHaystack lists the same texts.
func (e TaskView) Texts() []string {
	out := []string{e.Title, e.Notes, e.State, e.Kind, e.Area, e.ProjectTitle}
	out = append(out, e.Contexts...)
	for i, u := range e.Units {
		out = append(out, e.UnitLabel(i), string(u.Provider)+":"+u.Key)
	}
	if w := e.Waiting; w != nil {
		for _, p := range w.On {
			out = append(out, p.Name)
		}
	}
	for _, it := range e.Items {
		out = append(out, it.Summary)
	}
	for _, c := range e.Cards {
		if h := c.Card.LastHuman; h != nil {
			out = append(out, cmp.Or(h.Who.Name, h.Who.Key))
		}
		for _, p := range c.Card.WaitsOn {
			out = append(out, cmp.Or(p.Name, p.Key))
		}
	}
	if d := e.Draft; d != nil {
		out = append(out, d.Target, d.Where, d.Text, d.Reaction, d.Unit)
	}
	if m := e.Message; m != nil {
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
