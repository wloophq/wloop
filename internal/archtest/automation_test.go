package archtest

import (
	"fmt"
	"go/ast"
	"go/token"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/wloophq/wloop/internal/domain/unit"
)

// engineBans are the provider-specific packages the engine never imports:
// providers are values its ports carry.
var engineBans = []string{"internal/domain/pr", "internal/domain/turn", "internal/domain/view",
	"internal/domain/workspace", "internal/domain/round", "internal/adapter"}

var providerIDs = map[string]bool{string(unit.GitHub): true, string(unit.Session): true, string(unit.Workspace): true}

// engineImports flags an import of engineBans by internal/core/automation;
// its fakes, automationtest, are excluded.
func engineImports(self string, imports []string) []string {
	if self != "internal/core/automation" {
		return nil
	}
	var out []string
	for _, imp := range imports {
		if dep, internal := local(imp); internal && under(dep, engineBans...) {
			out = append(out, fmt.Sprintf("%s imports %s; the engine names no provider", self, imp))
		}
	}
	return out
}

// engineLiterals flags a provider id spelled in a non-test file of the
// engine, as a string literal or as a unit provider constant.
func engineLiterals(fset *token.FileSet, file string, f *ast.File) []string {
	if testFile(file) || filepath.Dir(file) != "internal/core/automation" {
		return nil
	}
	var out []string
	ast.Inspect(f, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.BasicLit:
			if s, err := strconv.Unquote(n.Value); err == nil && n.Kind == token.STRING && providerIDs[s] {
				out = append(out, fmt.Sprintf("%s: %q; the engine names no provider", fset.Position(n.Pos()), s))
			}
		case *ast.SelectorExpr:
			if x, ok := n.X.(*ast.Ident); ok && x.Name == "unit" && (n.Sel.Name == "GitHub" || n.Sel.Name == "Session" || n.Sel.Name == "Workspace") {
				out = append(out, fmt.Sprintf("%s: unit.%s; the engine names no provider", fset.Position(n.Pos()), n.Sel.Name))
			}
		}
		return true
	})
	return out
}

func TestTheEngineImportsNoProvider(t *testing.T) {
	t.Parallel()
	seen := false
	for _, p := range packages(t) {
		self, ok := local(p.ImportPath)
		if !ok {
			continue
		}
		seen = seen || self == "internal/core/automation"
		for _, b := range engineImports(self, p.Imports) {
			t.Error(b)
		}
		for _, name := range p.GoFiles {
			if self != "internal/core/automation" {
				continue
			}
			fset, f := parseFile(t, filepath.Join(p.Dir, name))
			for _, b := range engineLiterals(fset, self+"/"+name, f) {
				t.Error(b)
			}
		}
	}
	if !seen {
		t.Error("internal/core/automation is missing from the package list")
	}
}

func TestTheEngineRuleFlagsAPlantedProvider(t *testing.T) {
	t.Parallel()
	ok := []string{"context", module + "internal/core/port", module + "internal/domain/unit", module + "internal/domain/workflow"}
	flagged(t, "allowed", engineImports("internal/core/automation", ok), 0, "")
	bad := append(ok, module+"internal/domain/pr", module+"internal/domain/turn", module+"internal/domain/view",
		module+"internal/domain/workspace", module+"internal/domain/round", module+"internal/adapter/facts/github")
	flagged(t, "engine", engineImports("internal/core/automation", bad), 6, "names no provider")
	flagged(t, "its fakes", engineImports("internal/core/automation/automationtest", bad), 0, "")
	flagged(t, "another core package", engineImports("internal/core/turns", bad), 0, "")

	fset, f := parse(t, `package automation
var a = "github"
var b = unit.Session
var c = "sessions"
var d = workflow.Session
var e = unit.Unit{}
`)
	flagged(t, "literals", engineLiterals(fset, "internal/core/automation/x.go", f), 2, "names no provider")
	flagged(t, "a test file", engineLiterals(fset, "internal/core/automation/x_test.go", f), 0, "")
	flagged(t, "the fakes", engineLiterals(fset, "internal/core/automation/automationtest/x.go", f), 0, "")
}
