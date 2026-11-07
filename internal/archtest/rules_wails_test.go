package archtest

import (
	"fmt"
	"go/ast"
	"go/token"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

const (
	wailsModule  = "github.com/wailsapp/wails"
	wailsAdapter = "internal/client/window/wails"
)

// ruleWailsImport flags, in a file outside the window adapter, an import of
// Wails.
func ruleWailsImport(fset *token.FileSet, file string, f *ast.File) []string {
	if under(filepath.Dir(file), wailsAdapter) {
		return nil
	}
	var out []string
	for _, imp := range f.Imports {
		p, _ := strconv.Unquote(imp.Path.Value)
		if p == wailsModule || strings.HasPrefix(p, wailsModule+"/") {
			out = append(out, fmt.Sprintf("%s imports %s; only %s reaches Wails", fset.Position(imp.Pos()), p, wailsAdapter))
		}
	}
	return out
}

// TestOnlyTheWindowAdapterImportsWails checks that no Go file outside
// `internal/client/window/wails` imports Wails, test files and files a build
// tag leaves out of this build included: the window host stays one adapter
// behind the window client's port.
func TestOnlyTheWindowAdapterImportsWails(t *testing.T) {
	t.Parallel()
	scanned := 0
	for _, p := range packages(t) {
		self, ok := local(p.ImportPath)
		if !ok {
			continue
		}
		for _, name := range slices.Concat(p.GoFiles, p.TestGoFiles, p.XTestGoFiles, p.IgnoredGoFiles) {
			if !strings.HasSuffix(name, ".go") {
				continue
			}
			fset, f := parseFile(t, filepath.Join(p.Dir, name))
			scanned++
			for _, b := range ruleWailsImport(fset, self+"/"+name, f) {
				t.Error(b)
			}
		}
	}
	if scanned == 0 {
		t.Error("no Go file scanned")
	}
}
