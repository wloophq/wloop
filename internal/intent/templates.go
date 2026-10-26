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

	"github.com/wloophq/wloop/internal/brand"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	"github.com/wloophq/wloop/internal/intent/builtin"
	"github.com/wloophq/wloop/internal/intent/expr"
)

// ActionFuncs are the functions action texts may call.
var ActionFuncs = template.FuncMap{"join": strings.Join, "name": func() string { return brand.Name }}

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
	headline := func(path, name string, txt dintent.Text) {
		if txt.At.Line == 0 {
			return
		}
		if _, err := set.New(name).Parse(txt.Src); err != nil {
			_, msg := templateMsg(name, err)
			s.add(path, "%s", msg)
			ok = false
		}
	}
	headline("headline.resumed", HeadlineName(-1), d.Headline.Resumed)
	for i, c := range d.Headline.New {
		headline(fmt.Sprintf("headline.new[%d].text", i), HeadlineName(i), c.Text)
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
	headlinePaths := map[string]string{HeadlineName(-1): "headline.resumed"}
	for i := range d.Headline.New {
		headlinePaths[HeadlineName(i)] = fmt.Sprintf("headline.new[%d].text", i)
	}
	for _, t := range set.Templates() {
		if t.Tree == nil || (baseNames[t.Name()] && t.Tree.ParseName != file) {
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
			if p, ok := headlinePaths[tree.ParseName]; ok {
				s.add(p, "%s", msg)
				return
			}
			line, col := place(body, int(n.Position()), "."+chain[0])
			s.problems = append(s.problems, Problem{File: tree.ParseName, Line: line, Col: col, Msg: msg})
		})
	}
	return set
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

var actionTexts = []string{"reason_off", "confirm", "hint"}

// parseActionTexts parses reason_off, confirm and hint and renders each over a
// fully populated action fact map.
func (s *sheet) parseActionTexts(a *dintent.Action) map[string]*template.Template {
	if a == nil {
		return nil
	}
	sample := SampleActionFacts()
	out := map[string]*template.Template{}
	for _, k := range actionTexts {
		txt := map[string]dintent.Text{"reason_off": a.ReasonOff, "confirm": a.Confirm, "hint": a.Hint}[k]
		if txt.Src == "" {
			continue
		}
		path := "action." + k
		t, err := template.New(path).Funcs(ActionFuncs).Option("missingkey=error").Parse(txt.Src)
		if err == nil {
			err = t.Execute(io.Discard, sample)
		}
		if err != nil {
			_, msg := templateMsg(path, err)
			s.add(path, "%s", msg)
			continue
		}
		out[k] = t
	}
	return out
}

// ActionFactMap nests dotted action facts into the map action texts execute over:
// {"pr": {"slug": ...}, "row": {"nudge": {"who": ...}}}.
func ActionFactMap(get func(name string) any) map[string]any {
	root := map[string]any{}
	for _, f := range builtin.Facts {
		if !slices.Contains(f.Slots, builtin.ActionSlot) {
			continue
		}
		parts := strings.Split(f.Name, ".")
		m := root
		for _, p := range parts[:len(parts)-1] {
			next, ok := m[p].(map[string]any)
			if !ok {
				next = map[string]any{}
				m[p] = next
			}
			m = next
		}
		m[parts[len(parts)-1]] = get(f.Name)
	}
	return root
}

// SampleActionFacts is the action fact map with every fact set, for checking texts.
func SampleActionFacts() map[string]any {
	types := map[string]expr.Type{}
	for _, f := range builtin.Facts {
		if slices.Contains(f.Slots, builtin.ActionSlot) {
			types[f.Name] = f.Type
		}
	}
	return ActionFactMap(func(name string) any {
		switch types[name] {
		case expr.Bool:
			return true
		case expr.Int:
			return 1
		case expr.List:
			return []string{"sample"}
		default:
			return "sample"
		}
	})
}
