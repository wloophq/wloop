package archtest

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"slices"
	"strconv"
	"strings"
	"testing"
	"unicode"
)

const outboxPkg = module + "internal/core/outbox"

// pluginProposers are the files that still propose on their own: the jobs
// running a plugin's rules, GitHub's nudge.
var pluginProposers = map[string]string{
	"internal/core/proposals/proposals.go":        "the job that runs the plugins' own rules",
	"internal/adapter/source/github/proposals.go": "GitHub's nudge rule, github-nudge",
	"internal/plugins/source/broker_gates.go":     "a source plugin's own rule, which proposes through OpenGate with no session",
}

// draftTextAllowlist are the files whose drafts' text Go still writes.
var draftTextAllowlist = map[string]string{
	"internal/adapter/source/github/proposals.go": "GitHub's nudge text, github-nudge",
}

// TestDraftTextComesFromTheProfile checks that no Go code outside its
// allowlist writes a draft's text from prose of its own: the Text of an
// outbox.Proposal or a draft.Draft is never a string constant, a format, or
// what a function of the same package builds from string literals. A
// draft's text comes from a profile template or from whoever wrote it.
func TestDraftTextComesFromTheProfile(t *testing.T) {
	t.Parallel()
	fset, files := typedFiles(t)
	funcs := funcDecls(files)
	used := map[string]bool{}
	var hits []string
	for _, tf := range files {
		hs := draftTextHits(fset, tf.rel, tf.info, tf.file, funcs)
		if _, ok := draftTextAllowlist[tf.rel]; ok {
			used[tf.rel] = used[tf.rel] || len(hs) > 0
			continue
		}
		hits = append(hits, hs...)
	}
	for rel := range draftTextAllowlist {
		if !used[rel] {
			hits = append(hits, rel+": allowlisted but writes no draft text; drop it from draftTextAllowlist")
		}
	}
	slices.Sort(hits)
	for _, h := range hits {
		t.Error(h)
	}
}

// TestOnlyTheDraftStepProposes checks that only the draft.propose step, and
// the plugin proposers its allowlist names, build a draft.Origin of kind
// auto: every other draft comes from a person, a turn or an agent.
func TestOnlyTheDraftStepProposes(t *testing.T) {
	t.Parallel()
	fset, files := typedFiles(t)
	used := map[string]bool{}
	var hits []string
	for _, tf := range files {
		hs := autoOriginHits(fset, tf.rel, tf.info, tf.file)
		_, allowed := pluginProposers[tf.rel]
		switch {
		case tf.rel == "internal/cli/step_draftpropose.go":
		case allowed:
			used[tf.rel] = used[tf.rel] || len(hs) > 0
		default:
			hits = append(hits, hs...)
		}
	}
	for rel := range pluginProposers {
		if !used[rel] {
			hits = append(hits, rel+": allowlisted but builds no auto origin; drop it from pluginProposers")
		}
	}
	slices.Sort(hits)
	for _, h := range hits {
		t.Error(h)
	}
}

// funcDecls are the function declarations of files by their name's
// position, where a function's object points.
func funcDecls(files []typedFile) map[token.Pos]*ast.FuncDecl {
	out := map[token.Pos]*ast.FuncDecl{}
	for _, tf := range files {
		for _, d := range tf.file.Decls {
			if fd, ok := d.(*ast.FuncDecl); ok {
				out[fd.Name.Pos()] = fd
			}
		}
	}
	return out
}

// namedOf is the package path and name of e's type, its pointer's element
// for a pointer.
func namedOf(info *types.Info, e ast.Expr) (string, string) {
	tv, ok := info.Types[e]
	if !ok {
		return "", ""
	}
	t := tv.Type
	if p, ok := t.(*types.Pointer); ok {
		t = p.Elem()
	}
	n, ok := types.Unalias(t).(*types.Named)
	if !ok || n.Obj().Pkg() == nil {
		return "", ""
	}
	return n.Obj().Pkg().Path(), n.Obj().Name()
}

// textHolder says whether e is an outbox.Proposal or a draft.Draft.
func textHolder(info *types.Info, e ast.Expr) bool {
	p, n := namedOf(info, e)
	return p == outboxPkg && n == "Proposal" || p == draftPkg && n == "Draft"
}

func draftTextHits(fset *token.FileSet, rel string, info *types.Info, f *ast.File, funcs map[token.Pos]*ast.FuncDecl) []string {
	var out []string
	check := func(n ast.Node, v ast.Expr) {
		if why := proseOf(info, v, funcs); why != "" {
			out = append(out, fmt.Sprintf("%s:%d: a draft's text %s", rel, fset.Position(n.Pos()).Line, why))
		}
	}
	ast.Inspect(f, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.CompositeLit:
			if !textHolder(info, n) {
				return true
			}
			for _, el := range n.Elts {
				if kv, ok := el.(*ast.KeyValueExpr); ok {
					if k, ok := kv.Key.(*ast.Ident); ok && k.Name == "Text" {
						check(kv, kv.Value)
					}
				}
			}
		case *ast.AssignStmt:
			for i, lhs := range n.Lhs {
				if sel, ok := lhs.(*ast.SelectorExpr); ok && sel.Sel.Name == "Text" && textHolder(info, sel.X) && i < len(n.Rhs) {
					check(n, n.Rhs[i])
				}
			}
		}
		return true
	})
	return out
}

// proseOf says how v writes prose of its own: a constant with a letter, a
// format, or a call to a function of its package whose body holds a string
// literal with a letter outside an error; "" when it writes none.
func proseOf(info *types.Info, v ast.Expr, funcs map[token.Pos]*ast.FuncDecl) string {
	if tv, ok := info.Types[ast.Unparen(v)]; ok && tv.Value != nil && hasLetter(tv.Value.ExactString()) {
		return "is a string constant"
	}
	call, ok := ast.Unparen(v).(*ast.CallExpr)
	if !ok {
		return ""
	}
	var id *ast.Ident
	switch fn := call.Fun.(type) {
	case *ast.Ident:
		id = fn
	case *ast.SelectorExpr:
		id = fn.Sel
	}
	if id == nil {
		return ""
	}
	obj, ok := info.Uses[id].(*types.Func)
	if !ok || obj.Pkg() == nil {
		return ""
	}
	if obj.Pkg().Path() == "fmt" && strings.HasPrefix(obj.Name(), "Sprint") {
		return "is a format"
	}
	fd, ok := funcs[obj.Pos()]
	if !ok || fd.Body == nil {
		return ""
	}
	why := ""
	ast.Inspect(fd.Body, func(n ast.Node) bool {
		if c, ok := n.(*ast.CallExpr); ok && errorCall(c) {
			return false
		}
		if lit, ok := n.(*ast.BasicLit); ok && lit.Kind == token.STRING {
			if s, err := strconv.Unquote(lit.Value); err == nil && hasLetter(s) {
				why = "is built by " + obj.Name() + " from a string literal"
				return false
			}
		}
		return why == ""
	})
	return why
}

// errorCall says whether c builds an error, whose text no person reads in a
// draft.
func errorCall(c *ast.CallExpr) bool {
	sel, ok := c.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	x, ok := sel.X.(*ast.Ident)
	return ok && (x.Name == "fmt" && sel.Sel.Name == "Errorf" || x.Name == "errors" && sel.Sel.Name == "New")
}

func hasLetter(s string) bool { return strings.ContainsFunc(s, unicode.IsLetter) }

func autoOriginHits(fset *token.FileSet, rel string, info *types.Info, f *ast.File) []string {
	var out []string
	ast.Inspect(f, func(n ast.Node) bool {
		lit, ok := n.(*ast.CompositeLit)
		if !ok {
			return true
		}
		if p, name := namedOf(info, lit); p != draftPkg || name != "Origin" {
			return true
		}
		for _, el := range lit.Elts {
			kv, ok := el.(*ast.KeyValueExpr)
			if !ok {
				continue
			}
			if k, ok := kv.Key.(*ast.Ident); !ok || k.Name != "Kind" {
				continue
			}
			if tv, ok := info.Types[kv.Value]; ok && tv.Value != nil && tv.Value.ExactString() == `"auto"` {
				out = append(out, fmt.Sprintf("%s:%d: builds an auto draft origin outside draft.propose", rel, fset.Position(lit.Pos()).Line))
			}
		}
		return true
	})
	return out
}
