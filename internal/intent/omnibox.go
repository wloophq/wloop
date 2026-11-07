package intent

import (
	"fmt"
	"strings"

	"github.com/wloophq/wloop/internal/intent/builtin"
	"github.com/wloophq/wloop/internal/intent/expr"
	"github.com/wloophq/wloop/internal/profile"
)

// OmniboxFile is the profile file of the omnibox: how a search groups its results, and the known verbs.
const OmniboxFile = "omnibox.toml"

// OmniboxDef is omnibox.toml over every layer, its texts in the load's locale.
type OmniboxDef struct {
	// Limit is the most results a group shows.
	Limit int
	// Other labels the last group, of what no group's view holds.
	Other  string
	Groups []SearchGroupDef
}

// SearchGroupDef is a search group: the results its view holds.
type SearchGroupDef struct {
	ID, Label, View string
}

type mOmnibox struct {
	Search *mSearch `toml:"search"`
}

type mSearch struct {
	Limit  *int           `toml:"limit"`
	Other  *string        `toml:"other"`
	Groups []mSearchGroup `toml:"groups"`
}

type mSearchGroup struct {
	ID    string `toml:"id"`
	Label string `toml:"label"`
	View  string `toml:"view"`
}

type omniboxLoad struct {
	def     OmniboxDef
	viewsAt []ref
}

// loadOmnibox reads omnibox.toml from bundled and the user's layers: a higher layer's key replaces a lower
// one's, a list of groups the whole list.
func loadOmnibox(layers []profile.Layer, r layerReader) (*omniboxLoad, Problems) {
	l := &omniboxLoad{def: OmniboxDef{Limit: 5}}
	problems, _ := readLayered(layers, r, OmniboxFile, l.merge)
	return l, problems
}

func (l *omniboxLoad) merge(file string, src []byte) Problems {
	m, problems, _, complete := decode[mOmnibox](file, src)
	if !complete {
		return problems
	}
	s := &sheet{file: file, at: positions(src), problems: problems}
	if sr := m.Search; sr != nil {
		if sr.Limit != nil {
			if *sr.Limit < 1 {
				s.add("search.limit", "is at least 1")
			} else {
				l.def.Limit = *sr.Limit
			}
		}
		text(s, "search.other", sr.Other, &l.def.Other)
		if sr.Groups != nil {
			l.def.Groups, l.viewsAt = nil, nil
			for i, g := range sr.Groups {
				path := fmt.Sprintf("search.groups[%d]", i)
				def := SearchGroupDef{ID: strings.TrimSpace(g.ID), Label: strings.TrimSpace(g.Label), View: strings.TrimSpace(g.View)}
				if def.ID == "" || def.Label == "" || def.View == "" {
					continue
				}
				l.viewsAt = append(l.viewsAt, ref{s, path + ".view", def.View})
				l.def.Groups = append(l.def.Groups, def)
			}
		}
	}
	s.addGrammar(grammar(builtin.SurfaceOmnibox, src))
	return s.problems
}

// check leaves out the groups whose view the profile lacks, each a problem.
func (l *omniboxLoad) check(views ViewsDef) Problems {
	var out Problems
	ids := views.IDs()
	bad := map[string]bool{}
	for _, r := range l.viewsAt {
		if _, ok := views.View(r.name); !ok {
			out = append(out, r.s.problem(r.path, "names no view of %s%s", ViewsFile, expr.DidYouMean(r.name, ids)))
			bad[r.name] = true
		}
	}
	keep := l.def.Groups[:0]
	for _, g := range l.def.Groups {
		if !bad[g.View] {
			keep = append(keep, g)
		}
	}
	l.def.Groups = keep
	return out
}
