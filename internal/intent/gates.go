package intent

import (
	"io/fs"
	"slices"

	"github.com/wloophq/wloop/internal/domain/gate"
	"github.com/wloophq/wloop/internal/intent/builtin"
	"github.com/wloophq/wloop/internal/profile"
)

// GatesFile is the profile file that declares and words the gates.
const GatesFile = "gates.toml"

// GatesDef is the profile's gates.toml over every layer, in declared order
// as TasksDef's states are: bundled gates first, then each user layer's new
// ones; a gate's intents likewise.
type GatesDef struct {
	Gates []GateDef
}

// GateDef is a named gate: its kind, which names the consequence of
// resolving it, its words and the intents it is resolved with.
type GateDef struct {
	Name    string
	Kind    gate.Kind
	Label   string
	Intents []GateIntent
}

// GateIntent is what a gate is resolved with: the status it resolves the
// gate to, and whether it takes text.
type GateIntent struct {
	Name, Label string
	Status      gate.Status
	Answer      bool
}

// Gate is the gate name declares.
func (d GatesDef) Gate(name string) (GateDef, bool) {
	i := slices.IndexFunc(d.Gates, func(g GateDef) bool { return g.Name == name })
	if i < 0 {
		return GateDef{}, false
	}
	return d.Gates[i], true
}

// FirstOf is the first gate of kind k in the profile's order, which a gate
// the loop opens by kind takes.
func (d GatesDef) FirstOf(k gate.Kind) (GateDef, bool) {
	i := slices.IndexFunc(d.Gates, func(g GateDef) bool { return g.Kind == k })
	if i < 0 {
		return GateDef{}, false
	}
	return d.Gates[i], true
}

func (d GatesDef) names() []string {
	out := make([]string, len(d.Gates))
	for i, g := range d.Gates {
		out[i] = g.Name
	}
	return out
}

type mGatesFile struct {
	Gates map[string]mGate `toml:"gates"`
}

type mGate struct {
	Kind    *string                `toml:"kind"`
	Label   *string                `toml:"label"`
	Intents map[string]mGateIntent `toml:"intents"`
}

type mGateIntent struct {
	Label  *string `toml:"label"`
	Status *string `toml:"status"`
	Answer *bool   `toml:"answer"`
}

// loadGates reads gates.toml from bundled and the user's layers: a higher
// layer's key replaces a lower one's.
func loadGates(layers []profile.Layer, bundled fs.FS) (GatesDef, Problems) {
	var d GatesDef
	problems, _ := readLayered(layers, bundled, GatesFile, func(file string, src []byte) Problems {
		m, problems, _, complete := decode[mGatesFile](file, src)
		if !complete {
			return problems
		}
		s := &sheet{file: file, at: positions(src), problems: problems}
		for _, name := range declared(s, "gates", m.Gates) {
			mergeGate(s, &d, name, m.Gates[name])
		}
		s.addGrammar(grammar(builtin.SurfaceGates, src))
		return s.problems
	})
	return d, problems
}

func mergeGate(s *sheet, d *GatesDef, name string, m mGate) {
	path := "gates." + name
	i := slices.IndexFunc(d.Gates, func(g GateDef) bool { return g.Name == name })
	g := GateDef{Name: name}
	if i >= 0 {
		g = d.Gates[i]
		g.Intents = slices.Clone(g.Intents)
	}
	n := len(s.problems)
	switch {
	case m.Kind != nil:
		s.oneOf(path+".kind", *m.Kind, builtin.ValuesOf(builtin.SurfaceGates, "gates.*.kind"))
		g.Kind = gate.Kind(*m.Kind)
	case i < 0:
		s.add(path+".kind", "is required: what the gate decides")
	}
	text(s, path+".label", m.Label, &g.Label)
	for _, in := range declared(s, path+".intents", m.Intents) {
		mergeIntent(s, &g, path+".intents."+in, in, m.Intents[in])
	}
	if len(g.Intents) == 0 && len(s.problems) == n {
		s.add(path+".intents", "is required: a gate is resolved with one of its intents")
	}
	switch {
	case len(s.problems) > n:
	case i < 0:
		d.Gates = append(d.Gates, g)
	default:
		d.Gates[i] = g
	}
}

func mergeIntent(s *sheet, g *GateDef, path, name string, m mGateIntent) {
	i := slices.IndexFunc(g.Intents, func(in GateIntent) bool { return in.Name == name })
	in := GateIntent{Name: name}
	if i >= 0 {
		in = g.Intents[i]
	}
	n := len(s.problems)
	switch {
	case m.Status != nil:
		s.oneOf(path+".status", *m.Status, builtin.ValuesOf(builtin.SurfaceGates, "gates.*.intents.*.status"))
		in.Status = gate.Status(*m.Status)
	case i < 0:
		s.add(path+".status", "is required: the status the intent resolves the gate to")
	}
	text(s, path+".label", m.Label, &in.Label)
	if m.Answer != nil {
		in.Answer = *m.Answer
	}
	switch {
	case len(s.problems) > n:
	case i < 0:
		g.Intents = append(g.Intents, in)
	default:
		g.Intents[i] = in
	}
}
