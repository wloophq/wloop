package intent

import (
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"slices"
	"strings"

	dunit "github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/intent/builtin"
	"github.com/wloophq/wloop/internal/intent/expr"
	"github.com/wloophq/wloop/internal/profile"
)

// PredicatesFile sets the predicates a plugin's manifest declares, in the plugin's layers.
const PredicatesFile = "predicates.toml"

// Predicates are each plugin's predicates, by provider and name, as its layers set them.
type Predicates map[dunit.ProviderID]map[string]*expr.Program

// Holds says whether plugin p's predicate name holds over facts; an error when p declares no such predicate
// or no layer sets it, or when it fails to evaluate.
func (ps Predicates) Holds(p dunit.ProviderID, name string, facts map[string]any) (bool, error) {
	prg := ps[p][name]
	if prg == nil {
		return false, fmt.Errorf("plugin %s sets no predicate %s", p, name)
	}
	ok, err := prg.Holds(facts)
	if err != nil {
		return false, fmt.Errorf("plugin %s's predicate %s: %w", p, name, err)
	}
	return ok, nil
}

// loadPredicates reads predicates.toml from each plugin's layers: a higher layer's predicate replaces a lower
// one's, and one that does not compile over the plugin's facts leaves the lower one in force.
func loadPredicates(layers []profile.Layer, r layerReader) (Predicates, Problems) {
	out := Predicates{}
	var problems Problems
	for _, l := range layers {
		id := dunit.ProviderID(l.PluginID())
		if id == "" {
			continue
		}
		res, err := r.read(l, PredicatesFile)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			problems = append(problems, Problem{File: res.Path, Msg: err.Error()})
			continue
		}
		m, ps, _, complete := decode[map[string]any](res.Path, res.Body)
		problems = append(problems, ps...)
		if !complete {
			continue
		}
		s := &sheet{file: res.Path, at: positions(res.Body)}
		var declared []string
		for _, n := range builtin.PluginPredicates[id] {
			declared = append(declared, n.Name)
		}
		env := builtin.PluginFactsEnv(id)
		for _, name := range slices.Sorted(maps.Keys(m)) {
			if !slices.Contains(declared, name) {
				s.add(name, "plugin %s declares no predicate %s; it declares: %s", id, name, strings.Join(declared, ", "))
				continue
			}
			src, ok := m[name].(string)
			if !ok {
				s.add(name, "a predicate is a string, such as \"pr.owner == 'acme'\"")
				continue
			}
			prg, err := expr.Compile(src, env)
			if err != nil {
				msg, snippet := exprMsg(err)
				s.add(name, "%s", msg)
				s.problems[len(s.problems)-1].Snippet = snippet
				continue
			}
			if out[id] == nil {
				out[id] = map[string]*expr.Program{}
			}
			out[id][name] = prg
		}
		problems = append(problems, s.problems...)
	}
	return out, problems
}
