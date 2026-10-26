package archtest

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"slices"
	"strings"
	"testing"
)

const draftPkg = module + "internal/domain/draft"

// draftKindConsts are the kinds a draft took before purpose and delivery.
var draftKindConsts = []string{"Reply", "Nudge", "Close", "React", "AskOwner", "Comment"}

// draftShapeHolders are the types whose Purpose, Delivery and Kind fields
// name a draft's shape.
var draftShapeHolders = []string{draftPkg + ".Draft", module + "internal/core/outbox.Proposal", module + "internal/api.Draft"}

// deliveryReaders are the files that may compare a draft's delivery: the
// publishers and the Outbox's validation.
func deliveryReader(rel string) bool {
	return under(rel, "internal/core/outbox") || strings.HasPrefix(rel, "internal/adapter/source/") && strings.HasSuffix(rel, "/publisher.go")
}

// draftKindAllowlist are the files that may still read a draft's kind.
var draftKindAllowlist = map[string]string{}

// TestNoDraftKindComparisons checks that Go names no draft kind, compares
// no draft's purpose with a value nor keys a table by it, and compares its
// delivery only in the publishers and the Outbox: a draft is what its
// proposer declares.
func TestNoDraftKindComparisons(t *testing.T) {
	t.Parallel()
	fset, files := typedFiles(t)
	used := map[string]bool{}
	var hits []string
	for _, tf := range files {
		if under(tf.rel, "internal/domain/draft") {
			continue
		}
		hs := draftKindHits(fset, tf.rel, tf.info, tf.file)
		if _, ok := draftKindAllowlist[tf.rel]; ok {
			used[tf.rel] = used[tf.rel] || len(hs) > 0
			continue
		}
		hits = append(hits, hs...)
	}
	for rel := range draftKindAllowlist {
		if !used[rel] {
			hits = append(hits, rel+": allowlisted but reads no draft kind; drop it from draftKindAllowlist")
		}
	}
	slices.Sort(hits)
	for _, h := range hits {
		t.Error(h)
	}
}

// shapeField is the field of a draft's shape e selects: Purpose, Delivery
// or Kind; "" for anything else.
func shapeField(info *types.Info, e ast.Expr) string {
	sel, ok := ast.Unparen(e).(*ast.SelectorExpr)
	if !ok {
		return ""
	}
	s, ok := info.Selections[sel]
	if !ok || s.Kind() != types.FieldVal {
		return ""
	}
	recv := s.Recv()
	if p, ok := recv.(*types.Pointer); ok {
		recv = p.Elem()
	}
	n, ok := types.Unalias(recv).(*types.Named)
	if !ok || n.Obj().Pkg() == nil || !slices.Contains(draftShapeHolders, n.Obj().Pkg().Path()+"."+n.Obj().Name()) {
		return ""
	}
	switch name := sel.Sel.Name; name {
	case "Purpose", "Delivery", "Kind":
		return name
	}
	return ""
}

func namedDraftType(info *types.Info, e ast.Expr, name string) bool {
	tv, ok := info.Types[e]
	if !ok {
		return false
	}
	n, ok := types.Unalias(tv.Type).(*types.Named)
	return ok && n.Obj().Pkg() != nil && n.Obj().Pkg().Path() == draftPkg && n.Obj().Name() == name
}

// valued is e a non-empty constant value: a literal or a named constant.
func valued(info *types.Info, e ast.Expr) bool {
	tv, ok := info.Types[ast.Unparen(e)]
	return ok && tv.Value != nil && tv.Value.ExactString() != `""`
}

func draftKindHits(fset *token.FileSet, rel string, info *types.Info, f *ast.File) []string {
	var out []string
	hit := func(n ast.Node, format string, args ...any) {
		out = append(out, fmt.Sprintf("%s:%d: ", rel, fset.Position(n.Pos()).Line)+fmt.Sprintf(format, args...))
	}
	// compared reports what comparing x with y reads of a draft's kind, purpose or delivery.
	compared := func(x, y ast.Expr) string {
		switch field := shapeField(info, x); {
		case (field == "Kind" || namedDraftType(info, x, "Kind")) && valued(info, y):
			return "compares a draft's kind"
		case field == "Purpose" && valued(info, y):
			return "compares a draft's purpose with a value"
		case (field == "Delivery" || namedDraftType(info, x, "Delivery")) && valued(info, y) && !deliveryReader(rel):
			return "compares a draft's delivery outside the publishers and the Outbox"
		}
		return ""
	}
	ast.Inspect(f, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.Ident:
			if c, ok := info.Uses[n].(*types.Const); ok && c.Pkg() != nil && c.Pkg().Path() == draftPkg && slices.Contains(draftKindConsts, c.Name()) {
				hit(n, "names the draft kind %s", c.Name())
			}
		case *ast.IndexExpr:
			if tv, ok := info.Types[n.X]; ok {
				if _, isMap := tv.Type.Underlying().(*types.Map); isMap {
					if field := shapeField(info, n.Index); field == "Purpose" || field == "Kind" {
						hit(n, "keys a table by a draft's %s", strings.ToLower(field))
					}
				}
			}
		case *ast.BinaryExpr:
			if n.Op != token.EQL && n.Op != token.NEQ {
				return true
			}
			for _, pair := range [][2]ast.Expr{{n.X, n.Y}, {n.Y, n.X}} {
				if why := compared(pair[0], pair[1]); why != "" {
					hit(n, "%s", why)
					break
				}
			}
		case *ast.SwitchStmt:
			if n.Tag == nil {
				return true
			}
			for _, c := range n.Body.List {
				for _, e := range c.(*ast.CaseClause).List {
					if why := compared(n.Tag, e); why != "" {
						hit(e, "%s", why)
					}
				}
			}
		}
		return true
	})
	return out
}
