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
		module+"internal/github/githubtest", module+"internal/adapter/source/github", module+"internal/workspace/orca")
	flagged(t, "the daemon", ruleR8("internal/daemon", bad), 6, "(R8)")
	flagged(t, "a daemon subpackage", ruleR8("internal/daemon/feed", bad), 6, "(R8)")
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

// ruleR10Imports flags an import of internal/ghcli outside internal/github
// and the composition root, internal/cli.
func ruleR10Imports(self string, imports []string) []string {
	if under(self, "internal/github", "internal/ghcli", "internal/cli") {
		return nil
	}
	var out []string
	for _, imp := range imports {
		if dep, internal := local(imp); internal && under(dep, "internal/ghcli") {
			out = append(out, fmt.Sprintf("%s imports %s; only internal/github and internal/cli read the gh token (R10)", self, imp))
		}
	}
	return out
}

// ruleR10Literals flags, outside internal/github, a string literal naming a
// GitHub API endpoint.
func ruleR10Literals(fset *token.FileSet, file string, f *ast.File) []string {
	if under(path.Dir(file), "internal/github") {
		return nil
	}
	var out []string
	ast.Inspect(f, func(n ast.Node) bool {
		lit, ok := n.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return true
		}
		if s, err := strconv.Unquote(lit.Value); err == nil && githubAPI.MatchString(s) {
			out = append(out, fmt.Sprintf("%s: %q names GitHub's API; only internal/github reaches it (R10)", fset.Position(lit.Pos()), s))
		}
		return true
	})
	return out
}

func TestOnlyGitHubReachesGitHub(t *testing.T) {
	t.Parallel()
	seen := false
	for _, p := range packages(t) {
		self, ok := local(p.ImportPath)
		if !ok {
			continue
		}
		seen = seen || self == "internal/ghcli"
		for _, b := range ruleR10Imports(self, p.Imports) {
			t.Error(b)
		}
	}
	if !seen {
		t.Error("internal/ghcli is missing from the package list")
	}
	forEachFile(t, false, func(fset *token.FileSet, file string, f *ast.File) {
		for _, b := range ruleR10Literals(fset, file, f) {
			t.Error(b)
		}
	})
}

func TestR10FlagsAPlantedImport(t *testing.T) {
	t.Parallel()
	imports := []string{"net/http", module + "internal/github", module + "internal/ghcli"}
	for _, self := range []string{"internal/github", "internal/github/githubtest", "internal/cli", "internal/ghcli"} {
		flagged(t, self, ruleR10Imports(self, imports), 0, "")
	}
	for _, self := range []string{"internal/adapter/source/github", "internal/daemon", "internal/brief", "internal/clix"} {
		flagged(t, self, ruleR10Imports(self, imports), 1, "(R10)")
	}

	fset, f := parse(t, `package ghsource
const a = "https://api.github.com/graphql"
const b = "https://ghe.example.com/api/graphql"
const c = "https://ghe.example.com/api/v3/repos"
const d = "https://github.com/acme/api/pull/7"
const e = "/api/units/github/acme"
`)
	flagged(t, "a collector", ruleR10Literals(fset, "internal/adapter/source/github/collect.go", f), 3, "(R10)")
	flagged(t, "the client", ruleR10Literals(fset, "internal/github/client.go", f), 0, "")
	flagged(t, "a client test helper", ruleR10Literals(fset, "internal/github/githubtest/runner.go", f), 0, "")
}
