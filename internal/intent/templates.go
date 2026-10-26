package intent

import (
	"fmt"
	"io"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"text/template"
	"text/template/parse"
	"time"

	dintent "github.com/wloophq/wloop/internal/domain/intent"
	"github.com/wloophq/wloop/internal/intent/builtin"
	"github.com/wloophq/wloop/internal/intent/expr"
)

// HeadlineName is the name a headline text is parsed under in an intent's template set:
// "headline.resumed", "headline.new[0]", ...
func HeadlineName(i int) string {
	if i < 0 {
		return "headline.resumed"
	}
	return fmt.Sprintf("headline.new[%d]", i)
}

var lineCol = regexp.MustCompile(`^(\d+):(?:\d+:)? (.*)$`)

// templateMsg splits text/template's "template: name:line[:col]: msg".
func templateMsg(name string, err error) (line int, msg string) {
	rest, ok := strings.CutPrefix(err.Error(), "template: "+name+":")
	m := lineCol.FindStringSubmatch(rest)
	if !ok || m == nil {
		return 1, err.Error()
	}
	line, _ = strconv.Atoi(m[1])
	return line, strings.TrimPrefix(m[2], fmt.Sprintf("executing %q at ", name))
}

// parseMain parses the headlines and the instructions on a clone of base, then
// fails on any .Inputs.<x> the manifest does not declare.
func (s *sheet) parseMain(d dintent.Def, base *template.Template, file string, body string) *template.Template {
	set := template.New(file)
	if base != nil {
		clone, err := base.Clone()
		if err != nil {
			s.problems = append(s.problems, Problem{File: file, Line: 1, Col: 1, Msg: err.Error()})
			return nil
		}
		set = clone.New(file)
	}
	set.Option("missingkey=error")
	baseNames := map[string]bool{}
	if base != nil {
		for _, t := range base.Templates() {
			baseNames[t.Name()] = true
		}
	}
	ok := true
	text := func(path, name string, txt dintent.Text, env expr.Env) {
		if txt.At.Line == 0 {
			return
		}
		ok = s.parseText(set, path, name, txt, env) && ok
	}
	turn := TurnEnv(d)
	text("headline.resumed", HeadlineName(-1), d.Headline.Resumed, turn)
	for i, c := range d.Headline.New {
		text(fmt.Sprintf("headline.new[%d].line", i), HeadlineName(i), c.Text, turn)
	}
	unit := unitEnv(d)
	text("why", WhyName, d.Why, unit)
	for i, r := range d.Refuse {
		text(fmt.Sprintf("refuse[%d].why", i), RefuseWhyName(i), r.Why, unit)
	}
	if _, err := set.Parse(body); err != nil {
		line, msg := templateMsg(file, err)
		s.problems = append(s.problems, Problem{File: file, Line: line, Col: 1, Msg: msg})
		ok = false
	}
	if !ok {
		return nil
	}
	declared := map[string]bool{}
	for _, in := range d.Inputs {
		declared[in.Name] = true
	}
	inline := map[string]string{HeadlineName(-1): "headline.resumed", WhyName: "why"}
	for i := range d.Headline.New {
		inline[HeadlineName(i)] = fmt.Sprintf("headline.new[%d].line", i)
	}
	for i := range d.Refuse {
		inline[RefuseWhyName(i)] = fmt.Sprintf("refuse[%d].why", i)
	}
	for _, t := range set.Templates() {
		if t.Tree == nil || (baseNames[t.Name()] && t.Tree.ParseName != file) {
			continue
		}
		if p, ok := inline[t.Tree.ParseName]; ok {
			s.factReads(p, d, t.Tree)
			continue
		}
		walk(t.Tree.Root, func(n parse.Node, chain []string) {
			var msg string
			switch {
			case len(chain) >= 2 && chain[0] == "Inputs" && !declared[chain[1]]:
				msg = "inputs." + chain[1] + " is not declared"
			case builtin.PayloadProviders[chain[0]] != "" && d.Provider != builtin.PayloadProviders[chain[0]]:
				msg = fmt.Sprintf(".%s reads the %s pull request; a %s intent cannot read it", strings.Join(chain, "."), builtin.PayloadProviders[chain[0]], d.Provider)
			default:
				return
			}
			tree := t.Tree
			line, col := place(body, int(n.Position()), "."+chain[0])
			s.problems = append(s.problems, Problem{File: tree.ParseName, Line: line, Col: col, Msg: msg})
		})
	}
	return set
}

// factReads refuses a fact of another provider's that the inline template
// at path reads.
func (s *sheet) factReads(path string, d dintent.Def, tree *parse.Tree) {
	walk(tree.Root, func(_ parse.Node, chain []string) {
		for n := len(chain); n > 0; n-- {
			name := strings.Join(chain[:n], ".")
			if fp := builtin.FactProvider(name); fp != "" && !onlyServes(d, fp) {
				s.add(path, "%s is a %s fact; a %s intent cannot read it", name, fp, d.Provider)
				return
			}
		}
	})
}

// place is the 1-based line and column of the reference whose node sits at
// offset: text/template places a field chain at its last segment, so it backs
// up to root, such as ".Inputs", and to a leading "$".
func place(text string, offset int, root string) (int, int) {
	end := min(len(text), offset+len(root))
	start := strings.LastIndex(text[:end], root)
	if start < 0 {
		start = min(offset, len(text))
	}
	if start > 0 && text[start-1] == '$' {
		start--
	}
	nl := strings.LastIndex(text[:start], "\n")
	return 1 + strings.Count(text[:start], "\n"), start - nl
}

// walk calls f on every field chain of the context under n, .A.B and $.A.B alike.
func walk(n parse.Node, f func(parse.Node, []string)) {
	switch n := n.(type) {
	case *parse.ListNode:
		if n == nil {
			return
		}
		for _, c := range n.Nodes {
			walk(c, f)
		}
	case *parse.ActionNode:
		walk(n.Pipe, f)
	case *parse.PipeNode:
		if n == nil {
			return
		}
		for _, c := range n.Cmds {
			walk(c, f)
		}
	case *parse.CommandNode:
		for _, a := range n.Args {
			walk(a, f)
		}
	case *parse.IfNode:
		walkBranch(&n.BranchNode, f)
	case *parse.RangeNode:
		walkBranch(&n.BranchNode, f)
	case *parse.WithNode:
		walkBranch(&n.BranchNode, f)
	case *parse.TemplateNode:
		walk(n.Pipe, f)
	case *parse.ChainNode:
		walk(n.Node, f)
	case *parse.FieldNode:
		f(n, n.Ident)
	case *parse.VariableNode:
		if len(n.Ident) >= 2 && n.Ident[0] == "$" {
			f(n, n.Ident[1:])
		}
	}
}

func walkBranch(b *parse.BranchNode, f func(parse.Node, []string)) {
	walk(b.Pipe, f)
	walk(b.List, f)
	walk(b.ElseList, f)
}

// The names the manifest's other inline templates are parsed under in an
// intent's template set.
const WhyName = "manifest.why"

// RefuseWhyName is the name refuse[i].why is parsed under.
func RefuseWhyName(i int) string { return fmt.Sprintf("refuse[%d].why", i) }

// NestFacts nests the dotted facts of env into the map an inline template
// executes over, {"pr": {"slug": ...}}, each read by get; a fact get has
// no value for reads as its type's zero value.
func NestFacts(env expr.Env, get func(name string) any) map[string]any {
	root := map[string]any{}
	for _, name := range env.Names() {
		parts := strings.Split(name, ".")
		m := root
		ok := true
		for _, p := range parts[:len(parts)-1] {
			next, isMap := m[p].(map[string]any)
			if _, set := m[p]; set && !isMap {
				ok = false
				break
			}
			if !isMap {
				next = map[string]any{}
				m[p] = next
			}
			m = next
		}
		if !ok {
			continue
		}
		v := get(name)
		if v == nil {
			v = zero(env[name])
		}
		m[parts[len(parts)-1]] = v
	}
	return root
}

func zero(t expr.Type) any {
	switch t {
	case expr.Bool:
		return false
	case expr.Int:
		return 0
	case expr.List:
		return []string{}
	case expr.Duration:
		return time.Duration(0)
	case expr.Time:
		return time.Time{}
	}
	return ""
}

// TurnEnv is the env an intent's turn predicates and inline texts read:
// the turn slot's facts and its inputs, turn.stage only with the stage
// dimension.
func TurnEnv(d dintent.Def) expr.Env {
	env := builtin.Env(builtin.TurnSlot, d.Inputs)
	if !slices.Contains(d.Dims, dintent.DimStage) {
		delete(env, "turn.stage")
	}
	return env
}

// sample is the facts map of env with every fact set, for checking texts.
func sample(env expr.Env) map[string]any {
	return NestFacts(env, func(name string) any {
		switch env[name] {
		case expr.Bool:
			return true
		case expr.Int:
			return 1
		case expr.List:
			return []string{"sample"}
		case expr.Duration:
			return time.Hour
		case expr.Time:
			return time.Unix(0, 0).UTC()
		}
		return "sample"
	})
}

// parseText parses an inline template of d at path, under name in set, and
// renders it over a fully set facts map of env; a problem is placed at path.
func (s *sheet) parseText(set *template.Template, path, name string, txt dintent.Text, env expr.Env) bool {
	if txt.At.Line == 0 && txt.Src == "" {
		return true
	}
	t, err := set.New(name).Parse(txt.Src)
	if err == nil {
		err = t.Execute(io.Discard, sample(env))
	}
	if err != nil {
		_, msg := templateMsg(name, err)
		s.add(path, "%s", msg)
		return false
	}
	return true
}

// Why renders the why of the intent name over facts, the unit's workflow
// facts; "" for an intent without one.
func (l *Loaded) Why(name dintent.Name, facts map[string]any) (string, error) {
	d, ok := l.Registry.Get(name)
	if !ok || d.Why.Src == "" {
		return "", nil
	}
	return l.render(name, WhyName, unitEnv(d), facts)
}

// Refusal is the why of the intent name's first refusal whose when holds
// over facts, the unit's workflow facts; "" when none holds.
func (l *Loaded) Refusal(name dintent.Name, facts map[string]any) (string, error) {
	d, ok := l.Registry.Get(name)
	if !ok {
		return "", nil
	}
	p := l.Programs[name]
	for i := range d.Refuse {
		if i >= len(p.Refuse) || p.Refuse[i] == nil || !p.Refuse[i].Eval(mapFacts(facts)) {
			continue
		}
		return l.render(name, RefuseWhyName(i), unitEnv(d), facts)
	}
	return "", nil
}

func (l *Loaded) render(name dintent.Name, text string, env expr.Env, facts map[string]any) (string, error) {
	t := l.Templates[name].Lookup(text)
	if t == nil {
		return "", fmt.Errorf("%s: no %s", name, text)
	}
	var b strings.Builder
	if err := t.Execute(&b, NestFacts(env, func(n string) any { return facts[n] })); err != nil {
		return "", err
	}
	return b.String(), nil
}

type mapFacts map[string]any

func (m mapFacts) Get(name string) any { return m[name] }
