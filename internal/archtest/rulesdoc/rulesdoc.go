// Package rulesdoc reads the architecture rules from the test sources of
// internal/archtest and renders their reference page.
package rulesdoc

import (
	"cmp"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// SelfTestHelper is the helper every self-test calls; a test that calls it is
// no rule.
const SelfTestHelper = "flagged"

// Rule is a rule test: a test of the package that does not call SelfTestHelper.
type Rule struct {
	Test string
	File string
	// Rule is the sentence after "<Test> checks that " in the test's doc
	// comment, or empty when the comment is missing or worded otherwise.
	Rule       string
	Allowlists []Allowlist
}

// Allowlist is a package-level map[string]string, or slice of a struct of
// strings and ints, that a rule test reaches from its body.
type Allowlist struct {
	Name    string
	Entries []Entry
}

// Entry is one allowlist entry: its strings, and its reason and triage when
// the list has reasons.
type Entry struct {
	Item   []string
	Status Status
	Reason string
}

// Status is the triage of an allowlist entry against the architecture rules,
// read from its reason's prefix: "fix: " or "debt: ", none when it holds.
type Status string

const (
	Holds Status = "holds"
	// Fix is an exception that does not hold and whose fix is mechanical.
	Fix Status = "fix"
	// Debt is an exception that does not hold and whose fix is a refactor.
	Debt Status = "debt"
)

var symbols = map[Status]string{Holds: "✅", Fix: "🔧", Debt: "🧾"}

func triaged(item []string, reason string) Entry {
	if reason == "" {
		return Entry{Item: item}
	}
	for _, s := range []Status{Fix, Debt} {
		if rest, ok := strings.CutPrefix(reason, string(s)+": "); ok {
			return Entry{Item: item, Status: s, Reason: rest}
		}
	}
	return Entry{Item: item, Status: Holds, Reason: reason}
}

// Read returns the rule tests of the test files in dir, sorted by name.
func Read(dir string) ([]Rule, error) {
	paths, err := filepath.Glob(filepath.Join(dir, "*_test.go"))
	if err != nil {
		return nil, err
	}
	fset := token.NewFileSet()
	var files []*ast.File
	for _, p := range paths {
		f, err := parser.ParseFile(fset, p, nil, parser.ParseComments)
		if err != nil {
			return nil, err
		}
		files = append(files, f)
	}
	funcs, lists := declarations(files)
	var rules []Rule
	for i, f := range files {
		for _, d := range f.Decls {
			fn, ok := d.(*ast.FuncDecl)
			if !ok || fn.Recv != nil || !strings.HasPrefix(fn.Name.Name, "Test") || calls(fn, SelfTestHelper) {
				continue
			}
			r := Rule{Test: fn.Name.Name, File: filepath.Base(paths[i]), Rule: sentence(fn)}
			for _, name := range reached(fn, funcs, lists) {
				r.Allowlists = append(r.Allowlists, Allowlist{Name: name, Entries: lists[name]})
			}
			rules = append(rules, r)
		}
	}
	slices.SortFunc(rules, func(a, b Rule) int { return cmp.Compare(a.Test, b.Test) })
	return rules, nil
}

func declarations(files []*ast.File) (map[string]*ast.FuncDecl, map[string][]Entry) {
	funcs := map[string]*ast.FuncDecl{}
	structs := map[string][]string{}
	for _, f := range files {
		for _, d := range f.Decls {
			switch d := d.(type) {
			case *ast.FuncDecl:
				if d.Recv == nil {
					funcs[d.Name.Name] = d
				}
			case *ast.GenDecl:
				for _, s := range d.Specs {
					if ts, ok := s.(*ast.TypeSpec); ok {
						if fields, ok := stringFields(ts.Type); ok {
							structs[ts.Name.Name] = fields
						}
					}
				}
			}
		}
	}
	lists := map[string][]Entry{}
	for _, f := range files {
		for _, d := range f.Decls {
			g, ok := d.(*ast.GenDecl)
			if !ok || g.Tok != token.VAR {
				continue
			}
			for _, s := range g.Specs {
				vs := s.(*ast.ValueSpec)
				for i, v := range vs.Values {
					if lit, ok := v.(*ast.CompositeLit); ok {
						if entries := entries(lit, structs); len(entries) > 0 {
							lists[vs.Names[i].Name] = entries
						}
					}
				}
			}
		}
	}
	return funcs, lists
}

func stringFields(e ast.Expr) ([]string, bool) {
	st, ok := e.(*ast.StructType)
	if !ok {
		return nil, false
	}
	var names []string
	for _, f := range st.Fields.List {
		if id, ok := f.Type.(*ast.Ident); !ok || id.Name != "string" && id.Name != "int" {
			return nil, false
		}
		for _, n := range f.Names {
			names = append(names, n.Name)
		}
	}
	return names, len(names) > 0
}

func entries(lit *ast.CompositeLit, structs map[string][]string) []Entry {
	var out []Entry
	switch t := lit.Type.(type) {
	case *ast.MapType:
		if !isString(t.Key) || !isString(t.Value) {
			return nil
		}
		for _, e := range lit.Elts {
			kv := e.(*ast.KeyValueExpr)
			out = append(out, triaged([]string{text(kv.Key)}, text(kv.Value)))
		}
	case *ast.ArrayType:
		id, ok := t.Elt.(*ast.Ident)
		if !ok {
			return nil
		}
		fields, ok := structs[id.Name]
		if !ok {
			return nil
		}
		for _, e := range lit.Elts {
			out = append(out, entry(fields, e.(*ast.CompositeLit)))
		}
	}
	slices.SortFunc(out, func(a, b Entry) int {
		return cmp.Or(slices.Compare(a.Item, b.Item), cmp.Compare(a.Reason, b.Reason))
	})
	return out
}

func entry(fields []string, lit *ast.CompositeLit) Entry {
	values := map[string]string{}
	for i, e := range lit.Elts {
		if kv, ok := e.(*ast.KeyValueExpr); ok {
			values[kv.Key.(*ast.Ident).Name] = text(kv.Value)
		} else if i < len(fields) {
			values[fields[i]] = text(e)
		}
	}
	var item []string
	for _, f := range fields {
		if f != "reason" {
			item = append(item, values[f])
		}
	}
	return triaged(item, values["reason"])
}

func isString(e ast.Expr) bool {
	id, ok := e.(*ast.Ident)
	return ok && id.Name == "string"
}

func text(e ast.Expr) string {
	if lit, ok := e.(*ast.BasicLit); ok && lit.Kind == token.STRING {
		if s, err := strconv.Unquote(lit.Value); err == nil {
			return s
		}
	}
	return source(e)
}

// source renders a non-literal expression, such as a constant, as its source.
func source(e ast.Expr) string {
	switch e := e.(type) {
	case *ast.Ident:
		return e.Name
	case *ast.SelectorExpr:
		return source(e.X) + "." + e.Sel.Name
	case *ast.BinaryExpr:
		return source(e.X) + " " + e.Op.String() + " " + source(e.Y)
	case *ast.BasicLit:
		return e.Value
	}
	return fmt.Sprintf("%T", e)
}

func calls(fn *ast.FuncDecl, name string) bool {
	found := false
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		if c, ok := n.(*ast.CallExpr); ok {
			if id, ok := c.Fun.(*ast.Ident); ok && id.Name == name {
				found = true
			}
		}
		return !found
	})
	return found
}

func sentence(fn *ast.FuncDecl) string {
	if fn.Doc == nil {
		return ""
	}
	doc := strings.Join(strings.Fields(fn.Doc.Text()), " ")
	rest, ok := strings.CutPrefix(doc, fn.Name.Name+" checks that ")
	if !ok || rest == "" {
		return ""
	}
	r, size := utf8.DecodeRuneInString(rest)
	return string(unicode.ToUpper(r)) + rest[size:]
}

// reached lists, sorted, the allowlists fn's body names directly or through
// the package-level functions it calls.
func reached(fn *ast.FuncDecl, funcs map[string]*ast.FuncDecl, lists map[string][]Entry) []string {
	seen := map[string]bool{fn.Name.Name: true}
	found := map[string]bool{}
	var walk func(*ast.FuncDecl)
	walk = func(f *ast.FuncDecl) {
		ast.Inspect(f.Body, func(n ast.Node) bool {
			id, ok := n.(*ast.Ident)
			if !ok {
				return true
			}
			if _, ok := lists[id.Name]; ok {
				found[id.Name] = true
			}
			if g, ok := funcs[id.Name]; ok && !seen[id.Name] {
				seen[id.Name] = true
				walk(g)
			}
			return true
		})
	}
	walk(fn)
	out := make([]string, 0, len(found))
	for name := range found {
		out = append(out, name)
	}
	slices.Sort(out)
	return out
}

// Page renders the architecture rules reference page.
func Page(rules []Rule) string {
	var b strings.Builder
	b.WriteString(`---
title: Architecture rules
description: The architecture rules internal/archtest enforces, and the entries each allowlist lets pass.
---

Each rule is a test in ` + "`internal/archtest`" + `, with a self-test that plants a violation and expects it caught. [Testing](/architecture/testing/#the-architecture-tests) says how the rules work.

| Test | Rule |
|---|---|
`)
	for _, r := range rules {
		fmt.Fprintf(&b, "| `%s` | %s |\n", r.Test, cell(r.Rule))
	}
	b.WriteString("\n## Allowlists\n\nThe entries a rule lets pass, each with its reason. An entry that matches nothing fails its rule, so these lists only shrink. Status is the entry's triage against the architecture rules: ✅ the exception holds; 🔧 it does not, and the fix is mechanical; 🧾 it does not, and the fix is a refactor.\n")
	for _, r := range rules {
		if len(r.Allowlists) == 0 {
			continue
		}
		fmt.Fprintf(&b, "\n### `%s`\n", r.Test)
		for _, l := range r.Allowlists {
			reasons := slices.ContainsFunc(l.Entries, func(e Entry) bool { return e.Reason != "" })
			if reasons {
				fmt.Fprintf(&b, "\n| `%s` entry | Status | Reason |\n|---|---|---|\n", l.Name)
			} else {
				fmt.Fprintf(&b, "\n| `%s` entry |\n|---|\n", l.Name)
			}
			for _, e := range l.Entries {
				items := make([]string, len(e.Item))
				for i, it := range e.Item {
					items[i] = code(it)
				}
				if reasons {
					fmt.Fprintf(&b, "| %s | %s | %s |\n", strings.Join(items, " "), symbols[e.Status], cell(e.Reason))
				} else {
					fmt.Fprintf(&b, "| %s |\n", strings.Join(items, " "))
				}
			}
		}
	}
	b.WriteString("\nThis page is generated from the sources of `internal/archtest` by `make docs-ref`.\n")
	return b.String()
}

func cell(s string) string { return strings.ReplaceAll(s, "|", `\|`) }

func code(s string) string {
	fence := "`"
	for strings.Contains(s, fence) {
		fence += "`"
	}
	if strings.HasPrefix(s, "`") || strings.HasSuffix(s, "`") {
		return fence + " " + cell(s) + " " + fence
	}
	return fence + cell(s) + fence
}
