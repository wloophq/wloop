package archtest

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
)

const gatesPkg = module + "internal/core/gates"

// gateResolvers are the files that may close a gate besides the gates
// service: the store that implements the port and the migration that
// carries the version 6 decisions into it.
var gateResolvers = []string{
	"internal/core/gates/",
	"internal/adapter/store/sqlite/gates.go",
	"internal/adapter/store/sqlite/migrate_v7",
}

var gateStatusWrite = regexp.MustCompile(`(?is)\b(update|into)\s+gates\b.*\bstatus\b`)

// TestOnlyTheGatesServiceResolvesAGate checks that outside the gates
// service, the sqlite gates store and the version 7 migration no code calls
// Resolve on a gates.Store or writes gates.status in SQL: every decision is
// closed by the service, which accepts only a person, a trusted policy rule
// or a passed window.
func TestOnlyTheGatesServiceResolvesAGate(t *testing.T) {
	t.Parallel()
	fset, files := typedFiles(t)
	var hits []string
	for _, tf := range files {
		if slices.ContainsFunc(gateResolvers, func(p string) bool { return strings.HasPrefix(tf.rel, p) }) {
			continue
		}
		hits = append(hits, gateResolveHits(fset, tf.rel, tf.info, tf.file)...)
	}
	slices.Sort(hits)
	for _, h := range hits {
		t.Error(h)
	}
}

func gateResolveHits(fset *token.FileSet, rel string, info *types.Info, f *ast.File) []string {
	var out []string
	ast.Inspect(f, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.SelectorExpr:
			if storeResolve(info, n) {
				out = append(out, fmt.Sprintf("%s:%d: resolves a gate on the store; resolve it through gates.Service", rel, fset.Position(n.Pos()).Line))
			}
		case *ast.BasicLit:
			if s, err := strconv.Unquote(n.Value); n.Kind == token.STRING && err == nil && gateStatusWrite.MatchString(s) {
				out = append(out, fmt.Sprintf("%s:%d: writes gates.status in SQL; only the gates store does", rel, fset.Position(n.Pos()).Line))
			}
		}
		return true
	})
	return out
}

// storeResolve says whether sel is a Resolve method that takes a
// gates.Resolution, on anything but the gates service.
func storeResolve(info *types.Info, sel *ast.SelectorExpr) bool {
	s, ok := info.Selections[sel]
	if !ok || sel.Sel.Name != "Resolve" {
		return false
	}
	fn, ok := s.Obj().(*types.Func)
	if !ok {
		return false
	}
	params := fn.Signature().Params()
	takesResolution := false
	for i := range params.Len() {
		if n, ok := types.Unalias(params.At(i).Type()).(*types.Named); ok && n.Obj().Pkg() != nil && n.Obj().Pkg().Path() == gatesPkg && n.Obj().Name() == "Resolution" {
			takesResolution = true
		}
	}
	if !takesResolution {
		return false
	}
	recv := s.Recv()
	if p, ok := recv.(*types.Pointer); ok {
		recv = p.Elem()
	}
	n, ok := types.Unalias(recv).(*types.Named)
	return !ok || n.Obj().Pkg() == nil || n.Obj().Pkg().Path() != gatesPkg || n.Obj().Name() != "Service"
}
