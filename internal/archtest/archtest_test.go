package archtest

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
)

const module = "github.com/wloophq/wloop/"

type pkg struct {
	ImportPath   string
	Dir          string
	GoFiles      []string
	TestGoFiles  []string
	XTestGoFiles []string
	Imports      []string
	TestImports  []string
	XTestImports []string
	Standard     bool
	EmbedFiles   []string
}

var forbidden = []string{"os/exec", "net/http", "database/sql"}

var listed = sync.OnceValues(listPackages)

// packages lists the module's packages once per test binary.
func packages(t *testing.T) []pkg {
	t.Helper()
	all, err := listed()
	if err != nil {
		t.Fatal(err)
	}
	return all
}

func listPackages() ([]pkg, error) {
	cmd := exec.Command("go", "list", "-json", "./...")
	cmd.Dir = filepath.Join("..", "..")
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	var all []pkg
	dec := json.NewDecoder(strings.NewReader(string(out)))
	for dec.More() {
		var p pkg
		if err := dec.Decode(&p); err != nil {
			return nil, err
		}
		p.Imports = slices.DeleteFunc(p.Imports, func(imp string) bool { return imp == module+"internal/brand" })
		all = append(all, p)
	}
	return all, nil
}

var (
	fileSet = token.NewFileSet()
	parsed  sync.Map
)

// parseFile parses the Go file at path once per test binary, into fileSet.
func parseFile(t *testing.T, path string) (*token.FileSet, *ast.File) {
	t.Helper()
	f, err := parseOnce(path)
	if err != nil {
		t.Fatal(err)
	}
	return fileSet, f
}

func parseOnce(path string) (*ast.File, error) {
	parse, _ := parsed.LoadOrStore(path, sync.OnceValues(func() (*ast.File, error) {
		return parser.ParseFile(fileSet, path, nil, 0)
	}))
	return parse.(func() (*ast.File, error))()
}

func local(path string) (string, bool) { return strings.CutPrefix(path, module) }

func std(path string) bool { return !strings.Contains(strings.Split(path, "/")[0], ".") }

func under(path string, roots ...string) bool {
	return slices.ContainsFunc(roots, func(r string) bool { return path == r || strings.HasPrefix(path, r+"/") })
}

func TestArchitecture(t *testing.T) {
	t.Parallel()
	for _, p := range packages(t) {
		self, ok := local(p.ImportPath)
		if !ok {
			continue
		}
		var broken []string
		for _, imp := range p.Imports {
			dep, internal := local(imp)
			switch {
			case (imp == "modernc.org/sqlite" || imp == "database/sql") && !under(self, "internal/adapter/store"):
				broken = append(broken, fmt.Sprintf("%s imports %s; only the store may (C1)", self, imp))
			case under(self, "internal/domain") && !std(imp) && !(internal && under(dep, "internal/domain")):
				broken = append(broken, fmt.Sprintf("%s imports %s; the domain imports only the standard library and the domain (C3)", self, imp))
			case under(self, "internal/client") && internal && !under(dep, "internal/api", "internal/domain", "internal/client", "internal/termfmt"):
				broken = append(broken, fmt.Sprintf("%s imports %s; a client imports only the API and internal/termfmt (C2)", self, imp))
			case under(self, "internal/brief") && (imp == "os/exec" || (internal && under(dep, "internal/adapter/agent"))):
				broken = append(broken, fmt.Sprintf("%s imports %s; the brief runs the agent only through turns.Asker (C4)", self, imp))
			case under(self, "internal/brief") && internal && under(dep, "internal/adapter/store/sqlite"):
				broken = append(broken, fmt.Sprintf("%s imports %s; the brief reaches the store only through inventory (C4)", self, imp))
			case under(imp, "cel.dev/cel-go") && self != "internal/intent/expr":
				broken = append(broken, fmt.Sprintf("%s imports %s; only internal/intent/expr compiles CEL", self, imp))
			case under(self, "internal/api") && !std(imp) && !(internal && under(dep, "internal/domain", "internal/api")):
				broken = append(broken, fmt.Sprintf("%s imports %s; the API imports only the domain (C9)", self, imp))
			}
		}
		for _, b := range broken {
			t.Error(b)
		}
		if under(self, "internal/core") {
			checkCoreFiles(t, self, p)
		}
	}
}

// ruleR12 flags an import of a package under internal/meter that is neither
// the standard library nor internal/meter.
func ruleR12(self string, imports []string) []string {
	if !under(self, "internal/meter") {
		return nil
	}
	var out []string
	for _, imp := range imports {
		if dep, internal := local(imp); std(imp) || (internal && under(dep, "internal/meter")) {
			continue
		}
		out = append(out, fmt.Sprintf("%s imports %s; the meter imports only the standard library (R12)", self, imp))
	}
	return out
}

func TestMeterIsALeaf(t *testing.T) {
	t.Parallel()
	seen := false
	for _, p := range packages(t) {
		self, ok := local(p.ImportPath)
		if !ok {
			continue
		}
		seen = seen || self == "internal/meter"
		for _, b := range ruleR12(self, p.Imports) {
			t.Error(b)
		}
	}
	if !seen {
		t.Error("internal/meter is missing from the package list")
	}
}

func TestProvidersStayInAdaptersAndTheRoot(t *testing.T) {
	t.Parallel()
	used := map[int]bool{}
	for _, p := range packages(t) {
		self, ok := local(p.ImportPath)
		if !ok {
			continue
		}
		if self == "internal/brand" {
			continue
		}
		for _, name := range p.GoFiles {
			fset, f := parseFile(t, filepath.Join(p.Dir, name))
			path := self + "/" + name
			for _, b := range slices.Concat(ruleR1(path, f), ruleR2(fset, path, f, allowlist, used), ruleR3(path, f)) {
				t.Error(b)
			}
		}
	}
	for _, s := range stale(allowlist, used) {
		t.Error(s)
	}
}

func checkCoreFiles(t *testing.T, self string, p pkg) {
	for _, name := range p.GoFiles {
		_, f := parseFile(t, filepath.Join(p.Dir, name))
		ast.Inspect(f, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.TypeSpec:
				if it, ok := n.Type.(*ast.InterfaceType); ok && n.Name.IsExported() && len(it.Methods.List) > 5 {
					t.Errorf("%s.%s has %d methods; a port has at most five (C4)", self, n.Name, len(it.Methods.List))
				}
			}
			return true
		})
	}
}

// ruleNoGate flags an import of the round gate, which the workflows replaced:
// two automation mechanisms never live side by side.
func ruleNoGate(self string, imports []string) []string {
	var out []string
	for _, imp := range imports {
		if dep, internal := local(imp); internal && under(dep, "internal/gate") {
			out = append(out, fmt.Sprintf("%s imports %s; the workflows are the only automation", self, imp))
		}
	}
	return out
}

func TestNothingImportsTheGate(t *testing.T) {
	t.Parallel()
	for _, p := range packages(t) {
		self, ok := local(p.ImportPath)
		if !ok {
			continue
		}
		if self == "internal/gate" {
			t.Error("internal/gate is back")
		}
		for _, b := range ruleNoGate(self, slices.Concat(p.Imports, p.TestImports, p.XTestImports)) {
			t.Error(b)
		}
	}
	flagged(t, "a planted import", ruleNoGate("internal/cli", []string{"context", module + "internal/gate"}), 1, "the workflows are the only automation")
}
