package archtest

import (
	"fmt"
	"go/ast"
	"go/importer"
	"go/token"
	"go/types"
	"path"
	"regexp"
	"strconv"
	"testing"
)

// daemonBans are what the daemon reaches only through the ports cli/serve.go
// wires.
var daemonBans = []string{"internal/adapter", "internal/github", "internal/workspace/orca"}

// ruleR8 flags an import of an adapter, GitHub or the Orca
// client by the daemon.
func ruleR8(self string, imports []string) []string {
	if !under(self, "internal/daemon") {
		return nil
	}
	var out []string
	for _, imp := range imports {
		if dep, internal := local(imp); internal && under(dep, daemonBans...) {
			out = append(out, fmt.Sprintf("%s imports %s; the daemon reaches it through a port wired in cli/serve.go (R8)", self, imp))
		}
	}
	return out
}

// portMethods counts the methods of each interface type pkg declares,
// those of embedded interfaces included.
func portMethods(pkg *types.Package) map[string]int {
	out := map[string]int{}
	for _, name := range pkg.Scope().Names() {
		if tn, ok := pkg.Scope().Lookup(name).(*types.TypeName); ok {
			if it, ok := tn.Type().Underlying().(*types.Interface); ok {
				out[name] = it.NumMethods()
			}
		}
	}
	return out
}

// TestDaemonImports checks that the daemon reaches adapters, GitHub and Orca
// only through ports.
func TestDaemonImports(t *testing.T) {
	t.Parallel()
	seen := false
	for _, p := range packages(t) {
		self, ok := local(p.ImportPath)
		if !ok {
			continue
		}
		seen = seen || self == "internal/daemon"
		for _, b := range ruleR8(self, p.Imports) {
			t.Error(b)
		}
	}
	if !seen {
		t.Error("internal/daemon is missing from the package list")
	}
	daemon, err := newChecker(t, []string{module + "internal/daemon"}).check(module + "internal/daemon")
	if err != nil {
		t.Fatal(err)
	}
	counts := portMethods(daemon)
	for name, n := range counts {
		if n > 5 {
			t.Errorf("daemon.%s has %d methods; a port has at most five", name, n)
		}
	}
}

func TestR8FlagsAPlantedImport(t *testing.T) {
	t.Parallel()
	ok := []string{"context", "net/http", module + "internal/api", module + "internal/core/port", module + "internal/domain/inventory",
		module + "internal/domain/github", module + "internal/readmodel/prrow", module + "internal/workspace", module + "internal/brief"}
	flagged(t, "allowed", ruleR8("internal/daemon", ok), 0, "")
	bad := append(ok, module+"internal/adapter/store/inventory", module+"internal/adapter/store/sqlite", module+"internal/github",
		module+"internal/adapter/source/github", module+"internal/workspace/orca")
	flagged(t, "the daemon", ruleR8("internal/daemon", bad), 5, "(R8)")
	flagged(t, "a daemon subpackage", ruleR8("internal/daemon/feed", bad), 5, "(R8)")
	flagged(t, "the composition root", ruleR8("internal/cli", bad), 0, "")
	flagged(t, "a sibling of the daemon", ruleR8("internal/daemonx", bad), 0, "")

	fset, ports := parse(t, `package daemon
import "context"
type Small interface{ A(); B(); C(); D(); E() }
type Big interface{ A(); B(); C(); D(); E(); F() }
type Composite interface{ Small; Big; G() }
type Pair interface{ Small; context.Context }
`)
	conf := types.Config{Importer: importer.ForCompiler(fset, "source", nil)}
	pkg, err := conf.Check("ports", fset, []*ast.File{ports}, nil)
	if err != nil {
		t.Fatal(err)
	}
	got := portMethods(pkg)
	if got["Small"] != 5 || got["Big"] != 6 || got["Composite"] != 7 || got["Pair"] != 9 {
		t.Fatalf("methods: %v", got)
	}
}

// githubAPI names GitHub's API endpoints: the public host and the
// Enterprise Server paths.
var githubAPI = regexp.MustCompile(`api\.github\.com|/api/graphql|/api/v3\b`)

// ruleR10Literals flags, in the host, a string literal naming a GitHub
// API endpoint.
func ruleR10Literals(fset *token.FileSet, file string, f *ast.File) []string {
	if !under(path.Dir(file), "internal") {
		return nil
	}
	var out []string
	ast.Inspect(f, func(n ast.Node) bool {
		lit, ok := n.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return true
		}
		if s, err := strconv.Unquote(lit.Value); err == nil && githubAPI.MatchString(s) {
			out = append(out, fmt.Sprintf("%s: %q names GitHub's API; only the GitHub plugin reaches it (R10)", fset.Position(lit.Pos()), s))
		}
		return true
	})
	return out
}

// TestOnlyGitHubReachesGitHub checks that nothing in the host names
// GitHub's API: the GitHub plugin reaches it, with its own credentials.
func TestOnlyGitHubReachesGitHub(t *testing.T) {
	t.Parallel()
	forEachFile(t, false, func(fset *token.FileSet, file string, f *ast.File) {
		for _, b := range ruleR10Literals(fset, file, f) {
			t.Error(b)
		}
	})
}

func TestR10FlagsAPlantedLiteral(t *testing.T) {
	t.Parallel()
	fset, f := parse(t, `package ghsource
const a = "https://api.github.com/graphql"
const b = "https://ghe.example.com/api/graphql"
const c = "https://ghe.example.com/api/v3/repos"
const d = "https://github.com/acme/api/pull/7"
const e = "/api/units/github/acme"
`)
	flagged(t, "a host adapter", ruleR10Literals(fset, "internal/adapter/source/github/collect.go", f), 3, "(R10)")
	flagged(t, "the host's alias of a pull request", ruleR10Literals(fset, "internal/github/github.go", f), 3, "(R10)")
	flagged(t, "the plugin", ruleR10Literals(fset, "plugins/github/api/client.go", f), 0, "")
}
