package archtest

import (
	"fmt"
	"go/ast"
	"go/constant"
	"go/token"
	"go/types"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/wloophq/wloop/pkg/plugin/manifest"
)

// draftDeliveryConsts are the deliveries the draft package named before the manifests declared them.
var draftDeliveryConsts = []string{"Message", "Reaction", "CommentDelivery", "EditDelivery", "DeleteDelivery"}

// manifestNames are the names the bundled manifests declare that the core may not spell.
type manifestNames struct {
	// literal are tool names and write commands, refused in any string literal.
	literal []string
	// factPrefixes are draft.<provider>. of each bundled plugin, the prefix of its draft kinds' facts.
	factPrefixes []string
	// typed are draft kind and delivery names, refused only as a draft.Kind or draft.Delivery value.
	typed []string
}

func bundledManifestNames(t *testing.T) manifestNames {
	t.Helper()
	dirs, err := filepath.Glob(filepath.Join("..", "..", "plugins", "*", manifest.File))
	if err != nil || len(dirs) == 0 {
		t.Fatalf("no bundled manifest: %v", err)
	}
	var n manifestNames
	for _, p := range dirs {
		m, err := manifest.Load(filepath.Dir(p))
		if err != nil {
			t.Fatal(err)
		}
		s, err := m.Source()
		if err != nil {
			t.Fatal(err)
		}
		for _, tool := range s.Tools {
			n.literal = append(n.literal, tool.Name)
		}
		n.literal = append(n.literal, s.Session.WriteCommands...)
		n.factPrefixes = append(n.factPrefixes, "draft."+s.Provider+".")
		for _, k := range s.DraftKinds {
			n.typed = append(n.typed, k.Name)
		}
		for _, d := range s.Deliveries {
			n.typed = append(n.typed, d.Name)
		}
	}
	return n
}

// draftWord says whether e is a constant of type draft.Kind or draft.Delivery, and its value.
func draftWord(info *types.Info, e ast.Expr) (string, bool) {
	tv, ok := info.Types[e]
	if !ok || tv.Value == nil || tv.Value.Kind() != constant.String {
		return "", false
	}
	if !namedDraftType(info, e, "Kind") && !namedDraftType(info, e, "Delivery") {
		return "", false
	}
	return constant.StringVal(tv.Value), true
}

// manifestNameHits flags, in f, a string literal that is a tool name or a write command of a bundled manifest
// or starts with a bundled plugin's draft fact prefix, and a draft.Kind or draft.Delivery value that names a
// kind or a delivery a bundled manifest declares. A struct tag is no literal.
func manifestNameHits(fset *token.FileSet, rel string, info *types.Info, f *ast.File, n manifestNames) []string {
	var out []string
	seen := map[token.Pos]bool{}
	hit := func(at ast.Node, format string, args ...any) {
		if seen[at.Pos()] {
			return
		}
		seen[at.Pos()] = true
		out = append(out, fmt.Sprintf("%s:%d: ", rel, fset.Position(at.Pos()).Line)+fmt.Sprintf(format, args...))
	}
	tags := map[*ast.BasicLit]bool{}
	ast.Inspect(f, func(node ast.Node) bool {
		switch x := node.(type) {
		case *ast.Field:
			if x.Tag != nil {
				tags[x.Tag] = true
			}
		case *ast.ImportSpec:
			tags[x.Path] = true
		}
		return true
	})
	ast.Inspect(f, func(node ast.Node) bool {
		switch x := node.(type) {
		case *ast.BinaryExpr:
			if x.Op == token.EQL || x.Op == token.NEQ {
				for _, side := range [][2]ast.Expr{{x.X, x.Y}, {x.Y, x.X}} {
					if v, ok := stringConst(info, side[0]); ok && slices.Contains(n.typed, v) && draftString(info, side[1]) {
						hit(side[0], "compares a draft's kind or delivery with %q; it is its plugin manifest's", v)
					}
				}
			}
		case *ast.SwitchStmt:
			if x.Tag != nil && draftString(info, x.Tag) {
				for _, st := range x.Body.List {
					for _, e := range st.(*ast.CaseClause).List {
						if v, ok := stringConst(info, e); ok && slices.Contains(n.typed, v) {
							hit(e, "compares a draft's kind or delivery with %q; it is its plugin manifest's", v)
						}
					}
				}
			}
		case *ast.CompositeLit:
			var named []string
			for _, el := range x.Elts {
				parts := []ast.Expr{el}
				if kv, ok := el.(*ast.KeyValueExpr); ok {
					parts = []ast.Expr{kv.Key, kv.Value}
				}
				for _, p := range parts {
					if v, ok := stringConst(info, p); ok && slices.Contains(n.typed, v) && !slices.Contains(named, v) {
						named = append(named, v)
					}
				}
			}
			if len(named) >= 2 {
				hit(x, "lists the draft kinds or deliveries %q; they are their plugin manifests'", named)
			}
		}
		e, ok := node.(ast.Expr)
		if !ok {
			return true
		}
		if v, ok := draftWord(info, e); ok && slices.Contains(n.typed, v) {
			hit(e, "draft kind or delivery %q; it is its plugin manifest's", v)
			return false
		}
		lit, ok := e.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING || tags[lit] {
			return true
		}
		s, err := strconv.Unquote(lit.Value)
		if err != nil {
			return true
		}
		switch {
		case slices.Contains(n.literal, s):
			hit(lit, "%q is a tool or a command its plugin manifest declares", s)
		case slices.ContainsFunc(n.factPrefixes, func(p string) bool { return strings.HasPrefix(s, p) }):
			hit(lit, "%q is a draft fact its plugin manifest declares", s)
		}
		return true
	})
	return out
}

// stringConst is e's value when it is a string literal spelled in place, of any type.
func stringConst(info *types.Info, e ast.Expr) (string, bool) {
	if lit, ok := ast.Unparen(e).(*ast.BasicLit); !ok || lit.Kind != token.STRING {
		return "", false
	}
	tv, ok := info.Types[e]
	if !ok || tv.Value == nil || tv.Value.Kind() != constant.String {
		return "", false
	}
	return constant.StringVal(tv.Value), true
}

// draftString says whether e is string(x) of a draft.Kind or draft.Delivery x.
func draftString(info *types.Info, e ast.Expr) bool {
	call, ok := ast.Unparen(e).(*ast.CallExpr)
	if !ok || len(call.Args) != 1 {
		return false
	}
	if tv, ok := info.Types[call.Fun]; !ok || !tv.IsType() || tv.Type != types.Typ[types.String] {
		return false
	}
	return namedDraftType(info, call.Args[0], "Kind") || namedDraftType(info, call.Args[0], "Delivery")
}

// draftListHits flags, in f, a use of a delivery constant of the draft package outside it, and a list of draft
// kinds or deliveries spelled in Go.
func draftListHits(fset *token.FileSet, rel string, info *types.Info, f *ast.File) []string {
	var out []string
	ast.Inspect(f, func(node ast.Node) bool {
		switch x := node.(type) {
		case *ast.Ident:
			if c, ok := info.Uses[x].(*types.Const); ok && c.Pkg() != nil && c.Pkg().Path() == draftPkg && slices.Contains(draftDeliveryConsts, c.Name()) && !under(rel, "internal/domain/draft") {
				out = append(out, fmt.Sprintf("%s:%d: names the draft delivery %s", rel, fset.Position(x.Pos()).Line, c.Name()))
			}
		case *ast.CompositeLit:
			tv, ok := info.Types[x]
			if !ok {
				return true
			}
			sl, ok := tv.Type.Underlying().(*types.Slice)
			if !ok {
				return true
			}
			if n, ok := types.Unalias(sl.Elem()).(*types.Named); ok && n.Obj().Pkg() != nil && n.Obj().Pkg().Path() == draftPkg && (n.Obj().Name() == "Kind" || n.Obj().Name() == "Delivery") && len(x.Elts) > 0 {
				out = append(out, fmt.Sprintf("%s:%d: lists draft %s values in Go", rel, fset.Position(x.Pos()).Line, strings.ToLower(n.Obj().Name())))
			}
		}
		return true
	})
	return out
}

// TestArchNoDeliveryList checks that Go holds no list of draft deliveries: each comes from a plugin manifest.
// Outside the draft package nothing names its delivery constants, and no Go file outside the plugins lists
// draft kinds or deliveries.
func TestArchNoDeliveryList(t *testing.T) {
	fset, files := typedFiles(t)
	var hits []string
	for _, tf := range files {
		if under(tf.rel, "plugins") {
			continue
		}
		hits = append(hits, draftListHits(fset, tf.rel, tf.info, tf.file)...)
	}
	slices.Sort(hits)
	for _, h := range hits {
		t.Error(h)
	}
}

// manifestNameTrees are the trees that may not name what a plugin manifest declares: the core, the domain, the
// daemon, its clients, its transports and the agent adapters.
var manifestNameTrees = []string{"internal/core", "internal/domain", "internal/adapter/agent", "internal/daemon", "internal/cli", "internal/web",
	"internal/api", "internal/transport", "internal/client"}

// TestArchNoManifestNamesInCore checks that the core, the domain, the daemon, its clients, transports and API,
// the command line and the agent adapters name no provider-owned name a bundled plugin manifest declares: a
// tool, a write command or a draft fact (draft.<provider>.) in any string; a draft kind or delivery as a
// draft.Kind or draft.Delivery value, and as a string literal compared with a string of one, or listed with
// another such literal in one list or map.
func TestArchNoManifestNamesInCore(t *testing.T) {
	names := bundledManifestNames(t)
	fset, files := typedFiles(t)
	var hits []string
	for _, tf := range files {
		if !under(tf.rel, manifestNameTrees...) {
			continue
		}
		hits = append(hits, manifestNameHits(fset, tf.rel, tf.info, tf.file, names)...)
	}
	slices.Sort(hits)
	for _, h := range hits {
		t.Error(h)
	}
}
