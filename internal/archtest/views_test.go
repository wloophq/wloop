package archtest

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"slices"
	"testing"
)

// TestGoNamesNoView checks that Go never names a view: no Go constant, a literal or a conversion of one, is
// typed task.View outside internal/intent. The views and their ids are the profile's views.toml.
func TestGoNamesNoView(t *testing.T) {
	t.Parallel()
	fset, files := typedFiles(t)
	var hits []string
	for _, tf := range files {
		if !under(tf.rel, "internal/intent") {
			hits = append(hits, viewLiteralHits(fset, tf.rel, tf.info, tf.file)...)
		}
	}
	slices.Sort(hits)
	for _, h := range hits {
		t.Error(h)
	}
}

// viewLiteralHits are the constant expressions of f typed task.View but the empty one.
func viewLiteralHits(fset *token.FileSet, rel string, info *types.Info, f *ast.File) []string {
	var out []string
	ast.Inspect(f, func(n ast.Node) bool {
		e, ok := n.(ast.Expr)
		if !ok {
			return true
		}
		tv, ok := info.Types[e]
		if !ok || tv.Value == nil || tv.Value.ExactString() == `""` {
			return true
		}
		if named, ok := types.Unalias(tv.Type).(*types.Named); !ok || named.Obj().Pkg() == nil || named.Obj().Pkg().Path()+"."+named.Obj().Name() != taskPkg+".View" {
			return true
		}
		out = append(out, fmt.Sprintf("%s:%d: %s is a task.View constant; a view is the profile's", rel, fset.Position(e.Pos()).Line, tv.Value.ExactString()))
		return false
	})
	return out
}
