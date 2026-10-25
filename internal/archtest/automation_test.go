package archtest

import (
	"fmt"
	"go/ast"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/domain/unit"
)

// engineBans are the provider-specific packages the engine never imports:
// providers are values its ports carry.
var engineBans = []string{"internal/domain/pr", "internal/domain/turn", "internal/domain/view",
	"internal/domain/workspace", "internal/domain/round", "internal/adapter"}

var providerIDs = map[string]bool{string(unit.GitHub): true, string(unit.Session): true, string(unit.Workspace): true,
	string(unit.Entry): true, "slack": true}

// providerConsts are the unit provider constants the core may not spell.
var providerConsts = map[string]bool{"GitHub": true, "Session": true, "Workspace": true, "Entry": true}

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

// coreProviderAllowlist are the core files that may still name a provider.
var coreProviderAllowlist = []allowed{
	{"internal/core/port/port.go", "unit.GitHub", "debt: PROf reads a pull request from a github event; a pluginDebt edge"},
	{"internal/core/turns/headless.go", `"entry"`, "the headless turn's entry input, which shares the entry provider's spelling"},
}

// coreLiterals flags a provider id spelled in a non-test file of the
// core, as a string literal or as a unit provider constant; the core's fakes,
// packages named <x>test, are excluded. used marks the allowlist entries that
// matched.
func coreLiterals(fset *token.FileSet, file string, f *ast.File, allow []allowed, used map[int]bool) []string {
	dir := filepath.Dir(file)
	if testFile(file) || !under(dir, "internal/core") || strings.HasSuffix(filepath.Base(dir), "test") {
		return nil
	}
	var out []string
	flag := func(pos token.Pos, spelled string) {
		for i, a := range allow {
			if a.file == file && a.literal == spelled {
				used[i] = true
				return
			}
		}
		out = append(out, fmt.Sprintf("%s: %s; the core names no provider", fset.Position(pos), spelled))
	}
	ast.Inspect(f, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.BasicLit:
			if s, err := strconv.Unquote(n.Value); err == nil && n.Kind == token.STRING && providerIDs[s] {
				flag(n.Pos(), strconv.Quote(s))
			}
		case *ast.SelectorExpr:
			if x, ok := n.X.(*ast.Ident); ok && x.Name == "unit" && providerConsts[n.Sel.Name] {
				flag(n.Pos(), "unit."+n.Sel.Name)
			}
		}
		return true
	})
	return out
}

// TestTheCoreNamesNoProvider checks that the core spells no provider id.
func TestTheCoreNamesNoProvider(t *testing.T) {
	t.Parallel()
	used := map[int]bool{}
	forEachFile(t, false, func(fset *token.FileSet, file string, f *ast.File) {
		for _, b := range coreLiterals(fset, file, f, coreProviderAllowlist, used) {
			t.Error(b)
		}
	})
	for _, s := range stale(coreProviderAllowlist, used) {
		t.Error(s)
	}
}

// TestTheEngineImportsNoProvider checks that the automation engine imports no
// provider's domain.
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
var f = unit.Entry
var g = "entry"
`)
	none := map[int]bool{}
	flagged(t, "literals", coreLiterals(fset, "internal/core/automation/x.go", f, nil, none), 4, "the core names no provider")
	flagged(t, "another core package", coreLiterals(fset, "internal/core/gtd/x.go", f, nil, none), 4, "the core names no provider")
	flagged(t, "a test file", coreLiterals(fset, "internal/core/automation/x_test.go", f, nil, none), 0, "")
	flagged(t, "the fakes", coreLiterals(fset, "internal/core/automation/automationtest/x.go", f, nil, none), 0, "")
	flagged(t, "outside the core", coreLiterals(fset, "internal/daemon/x.go", f, nil, none), 0, "")
	allow := []allowed{{"internal/core/gtd/x.go", "unit.Session", "planted"}, {"internal/core/gtd/x.go", `"slack"`, "stale"}}
	used := map[int]bool{}
	flagged(t, "allowlisted", coreLiterals(fset, "internal/core/gtd/x.go", f, allow, used), 3, "the core names no provider")
	flagged(t, "stale", stale(allow, used), 1, "(stale)")
}
