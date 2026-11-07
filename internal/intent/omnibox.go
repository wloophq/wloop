package intent

import (
	"fmt"
	"maps"
	"slices"
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
	// Verbs are the known verbs, by id order of their first layer.
	Verbs []OmniboxVerbDef
}

// OmniboxVerbDef is a known verb: its words in every language and its target.
type OmniboxVerbDef struct {
	ID               string
	Words            []string
	Action, Verb, Op string
	actionAt, verbAt *sheet
}

// SearchGroupDef is a search group: the results its view holds.
type SearchGroupDef struct {
	ID, Label, View string
}

type mOmnibox struct {
	Search *mSearch                `toml:"search"`
	Verbs  map[string]mOmniboxVerb `toml:"verbs"`
}

type mOmniboxVerb struct {
	Words  []string `toml:"words"`
	Action *string  `toml:"action"`
	Verb   *string  `toml:"verb"`
	Op     *string  `toml:"op"`
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
	for _, id := range declared(s, "verbs", m.Verbs) {
		l.mergeVerb(s, id, m.Verbs[id])
	}
	s.addGrammar(grammar(builtin.SurfaceOmnibox, src))
	return s.problems
}

// mergeVerb lays verb id of a layer over the one below: its words replace the lower words, a target it sets
// replaces the lower targets.
func (l *omniboxLoad) mergeVerb(s *sheet, id string, m mOmniboxVerb) {
	path := "verbs." + id
	i := slices.IndexFunc(l.def.Verbs, func(v OmniboxVerbDef) bool { return v.ID == id })
	v := OmniboxVerbDef{ID: id}
	if i >= 0 {
		v = l.def.Verbs[i]
	}
	if m.Words != nil {
		v.Words = nil
		for _, w := range m.Words {
			if w = strings.TrimSpace(w); w != "" && !strings.ContainsAny(w, " \t") {
				v.Words = append(v.Words, w)
			} else {
				s.add(path+".words", "%q is no word: one word, no spaces", w)
			}
		}
	}
	if m.Action != nil || m.Verb != nil || m.Op != nil {
		v.Action, v.Verb, v.Op, v.actionAt, v.verbAt = "", "", "", nil, nil
		if m.Action != nil {
			v.Action, v.actionAt = strings.TrimSpace(*m.Action), s
		}
		if m.Verb != nil {
			v.Verb, v.verbAt = strings.TrimSpace(*m.Verb), s
		}
		if m.Op != nil {
			n := len(s.problems)
			s.oneOf(path+".op", *m.Op, builtin.OmniboxOps())
			if len(s.problems) == n {
				v.Op = *m.Op
			}
		}
	}
	switch {
	case len(v.Words) == 0:
		s.add(path+".words", "is required")
		return
	case v.Action != "" && (v.Verb != "" || v.Op != ""):
		s.add(path, "a verb runs an action, or a task verb with an op for text that names no task, not both")
		return
	case v.Action == "" && v.Verb == "" && v.Op == "":
		s.add(path, "needs a target: action, verb or op")
		return
	case v.Verb != "" && v.Op == builtin.OmniboxOps()[0].Name:
		s.add(path+".op", "a task verb's op is start")
		return
	}
	if i >= 0 {
		l.def.Verbs[i] = v
	} else {
		l.def.Verbs = append(l.def.Verbs, v)
	}
}

// checkVerbs leaves out the verbs whose action the catalog lacks, or whose task verb tasks.toml lacks or
// resolves a gate, each a problem.
func (l *omniboxLoad) checkVerbs(tasks TasksDef, actions []string) Problems {
	var out Problems
	keep := l.def.Verbs[:0]
	for _, v := range l.def.Verbs {
		path := "verbs." + v.ID
		switch {
		case v.Action != "" && !slices.Contains(actions, v.Action):
			out = append(out, v.actionAt.problem(path+".action", "%s is no catalog action%s", v.Action, expr.DidYouMean(v.Action, actions)))
			continue
		case v.Verb != "":
			verbs := slices.Sorted(maps.Keys(tasks.Verbs))
			if _, ok := tasks.Verbs[v.Verb]; !ok {
				out = append(out, v.verbAt.problem(path+".verb", "%s is no verb of %s%s", v.Verb, TasksFile, expr.DidYouMean(v.Verb, verbs)))
				continue
			}
			if h := tasks.Handler(v.Verb); slices.Contains(builtin.GateHandlers, h) {
				out = append(out, v.verbAt.problem(path+".verb", "%s resolves a gate (handler %s), which only the user does on the gate", v.Verb, h))
				continue
			}
		}
		keep = append(keep, v)
	}
	l.def.Verbs = keep
	return out
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
