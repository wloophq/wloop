package archtest

import (
	"fmt"
	"go/ast"
	"go/token"
	"slices"
	"testing"
)

// serveStarters are the functions of internal/cli that start serve's jobs on
// the context they are given.
var serveStarters = []string{"serveDeps", "serveAllDeps", "serveUntilConfigLoads"}

// ruleServeThroughTheHarness flags, in a test file of internal/cli other than
// harness_test.go, a call of a serve starter: the harness's testEnv.serveDeps
// binds the jobs it starts to the test, which cancels and waits for them
// before its stores and directories go away.
func ruleServeThroughTheHarness(fset *token.FileSet, file string, f *ast.File) []string {
	if !testFile(file) || !under(file, "internal/cli") || file == "internal/cli/harness_test.go" {
		return nil
	}
	var out []string
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if id, ok := call.Fun.(*ast.Ident); ok && slices.Contains(serveStarters, id.Name) {
			out = append(out, fmt.Sprintf("%s: calls %s; a test starts serve's jobs through testEnv.serveDeps, which stops and waits for them", fset.Position(call.Pos()), id.Name))
		}
		return true
	})
	return out
}

// TestServeRunsThroughTheHarness checks that a test of internal/cli starts
// serve's jobs only through the harness, so none outlives the test's files.
func TestServeRunsThroughTheHarness(t *testing.T) {
	t.Parallel()
	forEachFile(t, true, func(fset *token.FileSet, file string, f *ast.File) {
		for _, h := range ruleServeThroughTheHarness(fset, file, f) {
			t.Error(h)
		}
	})
}
