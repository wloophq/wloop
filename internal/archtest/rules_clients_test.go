package archtest

import (
	"fmt"
	"go/ast"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// pluginConsts are the unit provider constants of compiled-in plugins.
var pluginConsts = map[string]bool{"GitHub": true}

// ruleClientPlugin flags a client's Go file that names a plugin: a plugin's
// unit provider constant, or its id as a literal or a person key's prefix.
// A client reads what a provider can do from the served plugins.
func ruleClientPlugin(fset *token.FileSet, file string, f *ast.File, plugins []string) []string {
	if !under(filepath.Dir(file), "internal/client") || testFile(file) {
		return nil
	}
	imports := map[*ast.BasicLit]bool{}
	for _, imp := range f.Imports {
		imports[imp.Path] = true
	}
	var out []string
	ast.Inspect(f, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.BasicLit:
			s, err := strconv.Unquote(n.Value)
			if err != nil || n.Kind != token.STRING || imports[n] {
				return true
			}
			if prefix, _, _ := strings.Cut(s, ":"); slices.Contains(plugins, prefix) {
				out = append(out, fmt.Sprintf("%s: %q names a plugin; read the provider's capabilities from the served plugins", fset.Position(n.Pos()), s))
			}
		case *ast.SelectorExpr:
			if x, ok := n.X.(*ast.Ident); ok && x.Name == "unit" && pluginConsts[n.Sel.Name] {
				out = append(out, fmt.Sprintf("%s: unit.%s names a plugin; read the provider's capabilities from the served plugins", fset.Position(n.Pos()), n.Sel.Name))
			}
		}
		return true
	})
	return out
}

// TestClientsNameNoPlugin checks that no client names a plugin: the
// terminal, the menu bar and the other Go clients spell no plugin's id or
// provider constant, and the cockpit's scripts and page quote no plugin's id.
func TestClientsNameNoPlugin(t *testing.T) {
	t.Parallel()
	forEachFile(t, false, func(fset *token.FileSet, file string, f *ast.File) {
		for _, b := range ruleClientPlugin(fset, file, f, pluginIDs()) {
			t.Error(b)
		}
	})
	dir := filepath.Join("..", "..", webStatic)
	files, err := filepath.Glob(filepath.Join(dir, "*.html"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no page under %s: %v", webStatic, err)
	}
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, b := range ruleWebProvider(webStatic+"/"+filepath.Base(f), string(raw), pluginIDs()) {
			t.Error(b)
		}
	}
}

func TestClientPluginFlagsAPlantedName(t *testing.T) {
	t.Parallel()
	fset, f := parse(t, `package tui
var a = unit.GitHub
var b = "github"
var c = "slack:U1"
var d = unit.Session
var e = "session"
var g = "GitHub"
`)
	ids := []string{"github", "slack"}
	flagged(t, "a client", ruleClientPlugin(fset, "internal/client/tui/x.go", f, ids), 3, "names a plugin")
	flagged(t, "the menu bar", ruleClientPlugin(fset, "internal/client/menubar/x.go", f, ids), 3, "names a plugin")
	flagged(t, "a test", ruleClientPlugin(fset, "internal/client/tui/x_test.go", f, ids), 0, "")
	flagged(t, "not a client", ruleClientPlugin(fset, "internal/daemon/x.go", f, ids), 0, "")
}
