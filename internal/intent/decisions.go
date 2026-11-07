package intent

import (
	"errors"
	"io/fs"
	"maps"
	"slices"
	"strings"

	"github.com/wloophq/wloop/internal/domain/decision"
	"github.com/wloophq/wloop/internal/domain/gate"
	dunit "github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/i18n"
	"github.com/wloophq/wloop/internal/intent/builtin"
	"github.com/wloophq/wloop/internal/intent/expr"
	"github.com/wloophq/wloop/internal/profile"
)

// DecisionsFile maps gates, and each plugin's situations and signals, to decision kinds, and words each kind.
const DecisionsFile = "decisions.toml"

// Decisions are decisions.toml over every layer.
type Decisions struct {
	Map   decision.Map
	Kinds map[decision.Kind]KindTexts
	// Primary is what a queue offers first on a plugin's unit, by provider and kind: a catalog action or
	// builtin.DecisionOpen.
	Primary map[dunit.ProviderID]map[decision.Kind]string
	// Open is what a queue's button that opens a unit at its source says.
	Open string
}

// KindTexts are a decision kind's view and words; Primary is the gate intent a queue offers first, "" the
// gate's first.
type KindTexts struct {
	View         string
	Noun, Clause i18n.Forms
	Queue, Empty string
	Primary      string
	viewAt, prAt *sheet
}

type mDecisions struct {
	Open       *string           `toml:"open"`
	Gates      map[string]string `toml:"gates"`
	Situations map[string]string `toml:"situations"`
	Signals    map[string]string `toml:"signals"`
	Primary    map[string]string `toml:"primary"`
	Kinds      map[string]mKind  `toml:"kinds"`
}

type mKind struct {
	View    *string `toml:"view"`
	Primary *string `toml:"primary"`
	Queue   *string `toml:"queue"`
	Empty   *string `toml:"empty"`
	Noun    any     `toml:"noun"`
	Clause  any     `toml:"clause"`
}

type decisionsLoad struct {
	def       Decisions
	primaryAt map[dunit.ProviderID]map[decision.Kind]*sheet
}

// loadDecisions reads decisions.toml from the bundled layer, each plugin's layers and the user's: the
// default layers map gates and word the kinds, a plugin's layers map its situations and signals and set its
// primaries; a higher layer's key replaces a lower one's.
func loadDecisions(layers []profile.Layer, r layerReader) (*decisionsLoad, Problems) {
	l := &decisionsLoad{
		def: Decisions{
			Map: decision.Map{Gates: map[gate.Kind]decision.Kind{}, Situations: map[dunit.ProviderID]map[string]decision.Kind{},
				Signals: map[dunit.ProviderID]map[string]decision.Kind{}},
			Kinds:   map[decision.Kind]KindTexts{},
			Primary: map[dunit.ProviderID]map[decision.Kind]string{},
		},
		primaryAt: map[dunit.ProviderID]map[decision.Kind]*sheet{},
	}
	var problems Problems
	if r.bundled != nil {
		if src, err := r.readBundled(DecisionsFile); err == nil {
			problems = append(problems, l.merge("", DecisionsFile, src, r)...)
		} else if !errors.Is(err, fs.ErrNotExist) {
			problems = append(problems, Problem{File: DecisionsFile, Msg: err.Error()})
		}
	}
	for _, layer := range layers {
		if layer.Bundled() {
			continue
		}
		res, err := r.read(layer, DecisionsFile)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			problems = append(problems, Problem{File: res.Path, Msg: err.Error()})
			continue
		}
		problems = append(problems, l.merge(dunit.ProviderID(uiPlugin(layer)), res.Path, res.Body, r)...)
	}
	if r.texts != nil {
		for k, t := range l.def.Kinds {
			for key, dst := range map[string]*i18n.Forms{"noun": &t.Noun, "clause": &t.Clause} {
				if f, ok := r.texts.Forms(DecisionsFile, []string{"kinds", string(k), key}); ok && strings.TrimSpace(f["other"]) != "" {
					*dst = f
				}
			}
			l.def.Kinds[k] = t
		}
	}
	return l, problems
}

func (l *decisionsLoad) merge(plugin dunit.ProviderID, file string, src []byte, r layerReader) Problems {
	m, problems, _, complete := decode[mDecisions](file, src)
	if !complete {
		return problems
	}
	s := &sheet{file: file, at: positions(src), problems: problems, funcs: r.funcs()}
	kinds := builtin.DecisionKinds()
	kindOf := func(path, v string) (decision.Kind, bool) {
		n := len(s.problems)
		s.oneOf(path, v, kinds)
		return decision.Kind(v), len(s.problems) == n
	}
	if plugin == "" {
		text(s, "open", m.Open, &l.def.Open)
		for _, section := range []struct {
			name string
			set  bool
		}{{"situations", m.Situations != nil}, {"signals", m.Signals != nil}, {"primary", m.Primary != nil}} {
			if section.set {
				s.add(section.name, "%s belong to a plugin's layer, plugins/<id>/%s", section.name, DecisionsFile)
			}
		}
		for _, g := range declared(s, "gates", m.Gates) {
			path := "gates." + g
			if !slices.Contains(gate.Kinds, gate.Kind(g)) {
				names := make([]string, len(gate.Kinds))
				for i, k := range gate.Kinds {
					names[i] = string(k)
				}
				s.add(path, "%s is no gate kind%s", g, expr.DidYouMean(g, names))
				continue
			}
			if k, ok := kindOf(path, m.Gates[g]); ok {
				l.def.Map.Gates[gate.Kind(g)] = k
			}
		}
		for _, id := range declared(s, "kinds", m.Kinds) {
			if k, ok := kindOf("kinds."+id, id); ok {
				l.mergeKind(s, k, m.Kinds[id])
			}
		}
	} else {
		for _, section := range []struct {
			name string
			set  bool
		}{{"gates", m.Gates != nil}, {"kinds", m.Kinds != nil}, {"open", m.Open != nil}} {
			if section.set {
				s.add(section.name, "%s belong to the profile's own %s, not a plugin's layer", section.name, DecisionsFile)
			}
		}
		mapNames(s, "situations", m.Situations, builtin.PluginSituations[plugin], plugin, kindOf, l.def.Map.Situations)
		mapNames(s, "signals", m.Signals, builtin.PluginSignals[plugin], plugin, kindOf, l.def.Map.Signals)
		for _, id := range declared(s, "primary", m.Primary) {
			k, ok := kindOf("primary."+id, id)
			v := strings.TrimSpace(m.Primary[id])
			if !ok || v == "" {
				if ok {
					s.add("primary."+id, "is required")
				}
				continue
			}
			if l.def.Primary[plugin] == nil {
				l.def.Primary[plugin], l.primaryAt[plugin] = map[decision.Kind]string{}, map[decision.Kind]*sheet{}
			}
			l.def.Primary[plugin][k], l.primaryAt[plugin][k] = v, s
		}
	}
	s.addGrammar(grammar(builtin.SurfaceDecisions, src))
	return s.problems
}

// mapNames lays a plugin's section, names it declares mapped to kinds, over into.
func mapNames(s *sheet, section string, m map[string]string, names []string, plugin dunit.ProviderID,
	kindOf func(path, v string) (decision.Kind, bool), into map[dunit.ProviderID]map[string]decision.Kind) {
	for _, name := range declared(s, section, m) {
		path := section + "." + name
		if !slices.Contains(names, name) {
			s.add(path, "%s is no %s plugin %s declares; it declares %s%s", name, section, plugin, builtin.OrList(names), expr.DidYouMean(name, names))
			continue
		}
		if k, ok := kindOf(path, m[name]); ok {
			if into[plugin] == nil {
				into[plugin] = map[string]decision.Kind{}
			}
			into[plugin][name] = k
		}
	}
}

func (l *decisionsLoad) mergeKind(s *sheet, k decision.Kind, m mKind) {
	path := "kinds." + string(k)
	t, had := l.def.Kinds[k]
	required := func(key string, v *string, dst *string) {
		switch {
		case v != nil:
			text(s, path+"."+key, v, dst)
		case !had:
			s.add(path+"."+key, "is required")
		}
	}
	required("view", m.View, &t.View)
	if m.View != nil {
		t.viewAt = s
	}
	required("queue", m.Queue, &t.Queue)
	required("empty", m.Empty, &t.Empty)
	if m.Primary != nil {
		t.Primary, t.prAt = strings.TrimSpace(*m.Primary), s
	}
	for _, f := range []struct {
		key string
		v   any
		dst *i18n.Forms
	}{{"noun", m.Noun, &t.Noun}, {"clause", m.Clause, &t.Clause}} {
		if f.v == nil {
			continue
		}
		if forms, ok := formsOf(s, path+"."+f.key, f.v); ok {
			*f.dst = forms
		}
	}
	l.def.Kinds[k] = t
}

// check says what of the decisions names what the profile lacks: a kind's view, a kind's primary gate intent,
// a plugin's primary action.
func (l *decisionsLoad) check(views ViewsDef, gates GatesDef, actions map[string]BuiltinAction, offered func(dunit.ProviderID) []string) Problems {
	var out Problems
	viewIDs := views.IDs()
	var intents []string
	for _, g := range gates.Gates {
		for _, in := range g.Intents {
			if !slices.Contains(intents, in.Name) {
				intents = append(intents, in.Name)
			}
		}
	}
	for _, k := range decision.Kinds {
		t, ok := l.def.Kinds[k]
		if !ok {
			continue
		}
		if _, ok := views.View(t.View); !ok && t.viewAt != nil {
			out = append(out, t.viewAt.problem("kinds."+string(k)+".view", "names no view of %s%s", ViewsFile, expr.DidYouMean(t.View, viewIDs)))
		}
		if t.Primary != "" && !slices.Contains(intents, t.Primary) && t.prAt != nil {
			out = append(out, t.prAt.problem("kinds."+string(k)+".primary", "names no gate intent of %s%s", GatesFile, expr.DidYouMean(t.Primary, intents)))
			t.Primary = ""
			l.def.Kinds[k] = t
		}
	}
	for _, p := range slices.Sorted(maps.Keys(l.def.Primary)) {
		allowed := append([]string{builtin.DecisionOpen}, slices.Sorted(maps.Keys(actions))...)
		allowed = append(allowed, offered(p)...)
		for _, k := range decision.Kinds {
			v, ok := l.def.Primary[p][k]
			if !ok || slices.Contains(allowed, v) {
				continue
			}
			out = append(out, l.primaryAt[p][k].problem("primary."+string(k), "%s is no catalog action of plugin %s's units, nor %s%s", v, p, builtin.DecisionOpen, expr.DidYouMean(v, allowed)))
			delete(l.def.Primary[p], k)
		}
	}
	return out
}
