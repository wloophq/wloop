package intent

import (
	"fmt"
	"io/fs"
	"maps"
	"slices"
	"strings"
	"text/template"
	"time"

	"github.com/wloophq/wloop/internal/domain/workflow"
	"github.com/wloophq/wloop/internal/intent/expr"

	"github.com/wloophq/wloop/internal/domain/gate"
	"github.com/wloophq/wloop/internal/domain/task"
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
	// Approval is gates.toml's [approval]: how a gate is decided when no
	// manifest or configuration says, the default of each kind.
	Approval GateApproval
}

// GateApproval is gates.toml's [approval]: the first rule whose When holds
// over the gate's facts decides, else Mode and Window.
type GateApproval struct {
	Rules  []GateRule
	Mode   gate.Mode
	Window time.Duration
}

// GateRule is one rule of gates.toml's [approval], its When over the
// gate's facts only.
type GateRule struct {
	At     string
	When   workflow.Predicate
	Mode   gate.Mode
	Window time.Duration
}

// Decide is how a decides a gate whose facts are f: the mode, its window
// and where the rule that decided is declared.
func (a GateApproval) Decide(f workflow.Facts) (gate.Mode, time.Duration, string, error) {
	for _, r := range a.Rules {
		ok, err := r.When.Match(workflow.Vars{Facts: f})
		if err != nil {
			return gate.ModeWait, 0, r.At, fmt.Errorf("%s %s: %w", GatesFile, r.At, err)
		}
		if ok {
			return r.Mode, r.Window, GatesFile + " " + r.At, nil
		}
	}
	return a.Mode, a.Window, GatesFile + " approval.mode", nil
}

// DefaultFor is how gates.toml decides a gate of kind k: the first gate
// of that kind, open. A rule that cannot be read waits.
func (d GatesDef) DefaultFor(k gate.Kind) (gate.Mode, time.Duration, string) {
	g, _ := d.FirstOf(k)
	m, w, at, err := d.Approval.Decide(GateFacts(gate.Gate{Kind: k, Name: g.Name, Status: gate.Open}, ""))
	if err != nil || !m.AppliesTo(k) {
		return gate.ModeWait, 0, at
	}
	return m, w, at
}

// GateFacts are g's facts in slot gate, which a gates.toml rule and a
// view over gates read; delivery is a draft gate's delivery state, as the
// Outbox reads it from its draft, "" for any other gate.
func GateFacts(g gate.Gate, delivery string) workflow.Facts {
	return workflow.Facts{
		builtin.GateSlot + ".kind":     string(g.Kind),
		builtin.GateSlot + ".name":     g.Name,
		builtin.GateSlot + ".status":   string(g.Status),
		builtin.GateSlot + ".delivery": delivery,
	}
}

// gateEnv is the facts of slot gate.
func gateEnv() expr.Env {
	env := expr.Env{}
	for name, t := range builtin.WorkflowEnvAll() {
		if strings.HasPrefix(name, builtin.GateSlot+".") {
			env[name] = t
		}
	}
	return env
}

// GateDef is a named gate: its kind, which names the consequence of
// resolving it, its words and the intents it is resolved with.
type GateDef struct {
	Name    string
	Kind    gate.Kind
	Label   string
	Intents []GateIntent
	// EndsTask is the category, done or dropped, a task opened only to hold gates ends in once the gate closes
	// with a status and none of its gates is open; a status it leaves out leaves the task open.
	EndsTask map[gate.Status]task.Category
	// Text words the gate's subject when the loop writes it, a template
	// over the proposal facts; nil writes nothing.
	Text *template.Template
}

// Words is the gate's text over facts, "" when it has none.
func (g GateDef) Words(facts map[string]any) (string, error) {
	if g.Text == nil {
		return "", nil
	}
	var b strings.Builder
	err := g.Text.Execute(&b, facts)
	return strings.TrimSpace(b.String()), err
}

// ProposalFacts are the proposal facts, each value got by its name.
func ProposalFacts(get func(name string) any) map[string]any {
	return factMap(builtin.ProposalSlot, get)
}

// GateIntent is what a gate is resolved with: the status it resolves the
// gate to, and whether it takes text.
type GateIntent struct {
	Name, Label string
	// Summary is what resolving with it does, as help and palettes show it.
	Summary string
	Status  gate.Status
	Answer  bool
	// Texts are its note and confirm, templates over the action facts a
	// row offering it reads.
	Texts map[string]*template.Template
}

// Render is the intent's text name over facts, "" when it has none.
func (in GateIntent) Render(name string, facts map[string]any) (string, error) {
	return BuiltinAction{Texts: in.Texts}.Render(name, facts)
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
	Gates    map[string]mGate `toml:"gates"`
	Approval *mApproval       `toml:"approval"`
}

type mGate struct {
	Kind     *string                `toml:"kind"`
	Label    *string                `toml:"label"`
	Text     *string                `toml:"text"`
	Intents  map[string]mGateIntent `toml:"intents"`
	EndsTask map[string]string      `toml:"ends_task"`
}

type mGateIntent struct {
	Label   *string `toml:"label"`
	Summary *string `toml:"summary"`
	Note    *string `toml:"note"`
	Confirm *string `toml:"confirm"`
	Status  *string `toml:"status"`
	Answer  *bool   `toml:"answer"`
}

// BundledGates is the gates.toml of bundled alone.
func BundledGates(bundled fs.FS) (GatesDef, Problems) { return loadGates(nil, bundled) }

// loadGates reads gates.toml from bundled and the user's layers: a higher
// layer's key replaces a lower one's.
func loadGates(layers []profile.Layer, bundled fs.FS) (GatesDef, Problems) {
	var d GatesDef
	problems, read := readLayered(layers, bundled, GatesFile, func(file string, src []byte) Problems {
		m, problems, _, complete := decode[mGatesFile](file, src)
		if !complete {
			return problems
		}
		s := &sheet{file: file, at: positions(src), problems: problems}
		for _, name := range declared(s, "gates", m.Gates) {
			mergeGate(s, &d, name, m.Gates[name])
		}
		if m.Approval != nil {
			s.profileApproval(&d, m.Approval)
		}
		s.addGrammar(grammar(builtin.SurfaceGates, src))
		return s.problems
	})
	if !read {
		return d, problems
	}
	if d.Approval.Mode == "" {
		problems = append(problems, Problem{File: GatesFile, Field: "approval.mode", Msg: "is required: how a gate is decided when no rule holds"})
	}
	for _, k := range gate.Kinds {
		if _, ok := d.FirstOf(k); !ok {
			problems = append(problems, Problem{File: GatesFile, Field: "gates", Msg: fmt.Sprintf("declares no gate of kind %s: the loop opens one by its kind", k)})
		}
	}
	return d, problems
}

// profileApproval lays m over d's [approval]: a mode set replaces the
// mode and its window, rules set replace the rules, each over the gate's
// facts. A dry_run rule that pins gate.kind to another kind than draft is
// refused.
func (s *sheet) profileApproval(d *GatesDef, m *mApproval) {
	n := len(s.problems)
	a := d.Approval
	if m.Mode != nil || m.Window != nil {
		if mode, w, ok := s.modeWindow("approval", m.Mode, m.Window, builtin.GateModes(), "the gate's consequence"); ok {
			a.Mode, a.Window = gate.Mode(mode), w
		}
	}
	if m.Rule != nil {
		a.Rules = nil
	}
	env := gateEnv()
	for i, r := range m.Rule {
		path := fmt.Sprintf("approval.rule[%d]", i)
		mode, w, ok := s.modeWindow(path, r.Mode, r.Window, builtin.GateModes(), "the gate's consequence")
		if r.When == nil {
			s.add(path+".when", "is required: a predicate over the gate's facts")
			continue
		}
		p := s.predicate(path+".when", *r.When, env)
		if !ok || p == nil {
			continue
		}
		if k := nonDraftPin(p); gate.Mode(mode) == gate.ModeDryRun && k != "" {
			s.add(path+".mode", "is dry_run, which a %s gate never is: dry_run applies to draft gates only", k)
			continue
		}
		a.Rules = append(a.Rules, GateRule{At: path, When: p, Mode: gate.Mode(mode), Window: w})
	}
	if len(s.problems) == n {
		d.Approval = a
	}
}

// nonDraftPin is a kind other than draft p's when pins gate.kind to, "" for none.
func nonDraftPin(p workflow.Predicate) gate.Kind {
	for _, k := range pinnedKinds(p) {
		if k != gate.KindDraft {
			return k
		}
	}
	return ""
}

// pinnedKinds are the kinds p's top-level conjunction pins gate.kind to.
func pinnedKinds(p workflow.Predicate) []gate.Kind {
	var out []gate.Kind
	for _, k := range gate.Kinds {
		if expr.Requires(p, builtin.GateSlot+".kind", string(k)) {
			out = append(out, k)
		}
	}
	return out
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
	switch {
	case m.Text == nil:
	case *m.Text == "":
		g.Text = nil
	default:
		tmpl, err := ActionText(path+".text", *m.Text, sampleFacts(builtin.ProposalSlot))
		if err != nil {
			_, msg := templateMsg(path+".text", err)
			s.add(path+".text", "%s", msg)
		} else {
			g.Text = tmpl
		}
	}
	for _, in := range declared(s, path+".intents", m.Intents) {
		mergeIntent(s, &g, path+".intents."+in, in, m.Intents[in])
	}
	if m.EndsTask != nil {
		g.EndsTask = maps.Clone(g.EndsTask)
		if g.EndsTask == nil {
			g.EndsTask = map[gate.Status]task.Category{}
		}
		for _, st := range declared(s, path+".ends_task", m.EndsTask) {
			s.oneOf(path+".ends_task."+st, st, builtin.ValuesOf(builtin.SurfaceGates, "gates.*.intents.*.status"))
			s.oneOf(path+".ends_task."+st, m.EndsTask[st], builtin.ValuesOf(builtin.SurfaceGates, "gates.*.ends_task.*"))
			g.EndsTask[gate.Status(st)] = task.Category(m.EndsTask[st])
		}
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
	text(s, path+".summary", m.Summary, &in.Summary)
	in.Texts = maps.Clone(in.Texts)
	for _, t := range []struct {
		name string
		v    *string
	}{{"note", m.Note}, {"confirm", m.Confirm}} {
		name, v := t.name, t.v
		switch {
		case v == nil:
		case *v == "":
			delete(in.Texts, name)
		default:
			tmpl, err := ActionText(path+"."+name, *v, SampleActionFacts())
			if err != nil {
				_, msg := templateMsg(path+"."+name, err)
				s.add(path+"."+name, "%s", msg)
				continue
			}
			if in.Texts == nil {
				in.Texts = map[string]*template.Template{}
			}
			in.Texts[name] = tmpl
		}
	}
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
