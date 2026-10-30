package archtest

import (
	"fmt"
	"go/ast"
	"go/token"
	"path/filepath"
	"strconv"
	"testing"
)

// stdoutRoots are the packages a plugin process runs: the SDK and the plugins.
var stdoutRoots = []string{"pkg/plugin", "plugins"}

// stdoutUses are the identifiers, by import path, that write to stdout.
var stdoutUses = map[string][]string{
	"fmt":                   {"Print", "Printf", "Println"},
	"os":                    {"Stdout"},
	"syscall":               {"Stdout"},
	"golang.org/x/sys/unix": {"Stdout"},
}

// ruleStdout flags each use in f of an identifier that writes to stdout.
func ruleStdout(fset *token.FileSet, path string, f *ast.File) []string {
	names := map[string]string{}
	for _, imp := range f.Imports {
		p, _ := strconv.Unquote(imp.Path.Value)
		name := filepath.Base(p)
		if imp.Name != nil {
			name = imp.Name.Name
		}
		names[name] = p
	}
	var out []string
	ast.Inspect(f, func(n ast.Node) bool {
		if call, ok := n.(*ast.CallExpr); ok && opensFD1(call, names) {
			out = append(out, fmt.Sprintf("%s:%d: os.NewFile(1, ...) opens stdout, which carries the plugin handshake; log through plugin.Logger (R-stdout)", path, fset.Position(call.Pos()).Line))
		}
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		x, ok := sel.X.(*ast.Ident)
		if !ok {
			return true
		}
		for _, id := range stdoutUses[names[x.Name]] {
			if sel.Sel.Name == id {
				out = append(out, fmt.Sprintf("%s:%d: %s.%s writes to stdout, which carries the plugin handshake; log through plugin.Logger (R-stdout)", path, fset.Position(sel.Pos()).Line, x.Name, id))
			}
		}
		return true
	})
	return out
}

func opensFD1(call *ast.CallExpr, names map[string]string) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "NewFile" || len(call.Args) == 0 {
		return false
	}
	x, ok := sel.X.(*ast.Ident)
	lit, isLit := call.Args[0].(*ast.BasicLit)
	return ok && names[x.Name] == "os" && isLit && lit.Value == "1"
}

// TestPluginWritesNothingToStdout checks that no file of `pkg/plugin` or
// `plugins/` writes to stdout: go-plugin reads a plugin's handshake from it,
// so a plugin logs to stderr only.
func TestPluginWritesNothingToStdout(t *testing.T) {
	t.Parallel()
	seen := false
	for _, p := range packages(t) {
		self, ok := local(p.ImportPath)
		if !ok || !under(self, stdoutRoots...) {
			continue
		}
		seen = seen || self == "pkg/plugin"
		for _, name := range p.GoFiles {
			fset, f := parseFile(t, filepath.Join(p.Dir, name))
			for _, b := range ruleStdout(fset, self+"/"+name, f) {
				t.Error(b)
			}
		}
	}
	if !seen {
		t.Error("pkg/plugin is missing from the package list")
	}
}
