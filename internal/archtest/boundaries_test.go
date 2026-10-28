package archtest

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/intent/builtin"
)

const goToml = "github.com/pelletier/go-toml/v2"

// allowedImport says whether imp is allowed: the standard library always,
// a module package when it sits under one of roots, and goToml when toml is set.
func allowedImport(imp string, toml bool, roots ...string) bool {
	if std(imp) || (toml && under(imp, goToml)) {
		return true
	}
	dep, ok := local(imp)
	return ok && under(dep, roots...)
}

// catalogImports (R1) flags an import of internal/catalog/... beyond the
// standard library, the API, the domain and the intent vocabulary.
func catalogImports(pkg string, imports []string) []string {
	self, ok := local(pkg)
	if !ok || !under(self, "internal/catalog") {
		return nil
	}
	var out []string
	for _, imp := range imports {
		if imp != module+"internal/intent" && !allowedImport(imp, false, "internal/api", "internal/domain", "internal/intent/builtin", "internal/intent/expr", "internal/catalog", "internal/keys") {
			out = append(out, fmt.Sprintf("%s imports %s; the catalog reads facts, never the daemon, the cli or adapters", self, imp))
		}
	}
	return out
}

// leaves are the intent vocabulary's leaf packages, the module roots and the
// outside modules each may import.
var leaves = map[string]struct {
	toml  bool
	roots []string
	deps  []string
}{
	"internal/intent/expr":    {roots: []string{"internal/domain"}, deps: []string{"cel.dev/cel-go"}},
	"internal/intent/builtin": {roots: []string{"internal/domain", "internal/intent/expr", "pkg/plugin"}},
	"internal/frontmatter":    {toml: true},
	"internal/keys":           {},
}

// leafImports (R2) flags an import of a leaf of the intent vocabulary beyond
// what its entry in leaves allows.
func leafImports(pkg string, imports []string) []string {
	self, _ := local(pkg)
	leaf, ok := leaves[self]
	if !ok {
		return nil
	}
	var out []string
	for _, imp := range imports {
		if !allowedImport(imp, leaf.toml, leaf.roots...) && !under(imp, leaf.deps...) {
			out = append(out, fmt.Sprintf("%s imports %s; a leaf of the intent vocabulary depends on no layer above it", self, imp))
		}
	}
	return out
}

// loaderImports (R3) flags an import of the loader, internal/intent, beyond
// go-toml, the domain, the front matter, its own subpackages, the profile and
// the key map.
func loaderImports(pkg string, imports []string) []string {
	if pkg != module+"internal/intent" {
		return nil
	}
	var out []string
	for _, imp := range imports {
		if !allowedImport(imp, true, "internal/domain", "internal/frontmatter", "internal/intent", "internal/profile", "internal/keys") {
			out = append(out, fmt.Sprintf("internal/intent imports %s; the loader reads manifests and never reaches turn, the daemon, the cli, the catalog, the core or adapters", imp))
		}
	}
	return out
}

// termfmtDeps are the modules internal/termfmt may import beyond the standard library.
var termfmtDeps = []string{"golang.org/x/term", "github.com/mattn/go-runewidth", "github.com/clipperhouse/uax29/v2"}

// termfmtImports (R6) flags an import of internal/termfmt beyond the standard
// library and termfmtDeps.
func termfmtImports(pkg string, imports []string) []string {
	if pkg != module+"internal/termfmt" {
		return nil
	}
	var out []string
	for _, imp := range imports {
		if !std(imp) && !under(imp, termfmtDeps...) {
			out = append(out, fmt.Sprintf("internal/termfmt imports %s; it formats for a terminal and depends on nothing of the loop nor any module beyond the width and terminal ones", imp))
		}
	}
	return out
}

var actionTypes = []string{
	module + "internal/api.ActionState",
	module + "internal/api.ActionSpec",
	module + "internal/api.InputSpec",
}

func testFile(file string) bool { return strings.HasSuffix(file, "_test.go") }

// historicalMigration reports a store migration file, which keeps the words
// the version it migrates from stored.
func historicalMigration(file string) bool {
	return strings.HasPrefix(file, "internal/adapter/store/sqlite/migrate_v")
}

// actionLiterals (R4) flags a non-empty composite literal of an action spec,
// state or input spec in a non-test file outside internal/catalog. A zero
// value is allowed. Mutating a state received from the catalog is not checked.
func actionLiterals(fset *token.FileSet, file string, info *types.Info, f *ast.File) []string {
	if testFile(file) || under(path.Dir(file), "internal/catalog") {
		return nil
	}
	var out []string
	ast.Inspect(f, func(n ast.Node) bool {
		lit, ok := n.(*ast.CompositeLit)
		if !ok || len(lit.Elts) == 0 {
			return true
		}
		named, ok := types.Unalias(info.Types[lit].Type).(*types.Named)
		if !ok || named.Obj().Pkg() == nil {
			return true
		}
		if name := named.Obj().Pkg().Path() + "." + named.Obj().Name(); slices.Contains(actionTypes, name) {
			out = append(out, fmt.Sprintf("%s: builds an api.%s; only the catalog builds action specs and states", fset.Position(lit.Pos()), named.Obj().Name()))
		}
		return true
	})
	return out
}

var programFields = []string{"When", "Refuse", "Primary", "ActionTexts"}

// programField says whether v is one of programFields of internal/intent.Programs,
// or of its ActionPrograms.
func programField(v *types.Var) bool {
	if !v.IsField() || v.Pkg() == nil || v.Pkg().Path() != module+"internal/intent" || !slices.Contains(programFields, v.Name()) {
		return false
	}
	for _, name := range []string{"Programs", "ActionPrograms"} {
		obj := v.Pkg().Scope().Lookup(name)
		if obj == nil {
			continue
		}
		st, ok := obj.Type().Underlying().(*types.Struct)
		if !ok {
			continue
		}
		for i := range st.NumFields() {
			if st.Field(i) == v {
				return true
			}
		}
	}
	return false
}

// programReads (R5) flags a read of an action predicate or text of
// internal/intent.Programs in a non-test file outside the loader and the catalog.
func programReads(fset *token.FileSet, file string, info *types.Info, f *ast.File) []string {
	if testFile(file) || under(path.Dir(file), "internal/intent", "internal/catalog") {
		return nil
	}
	var out []string
	ast.Inspect(f, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		s, ok := info.Selections[sel]
		if !ok || s.Kind() != types.FieldVal {
			return true
		}
		if v, ok := s.Obj().(*types.Var); ok && programField(v) {
			out = append(out, fmt.Sprintf("%s: reads Programs.%s; only the loader and the catalog evaluate an intent's predicates", fset.Position(sel.Pos()), v.Name()))
		}
		return true
	})
	return out
}

// builtinTables are the files that may spell a builtin's dotted or snake name.
var builtinTables = []string{"internal/intent/builtin/builtin.go", "internal/intent/builtin/steps.go", "internal/intent/builtin/effects.go",
	"internal/intent/builtin/roles.go", "internal/cli/builtins.go", "internal/cli/builtins_effects.go",
	"internal/daemon/builtins.go", "internal/cli/steps.go", "internal/cli/root.go", "internal/intent/builtin/renamed.go"}

// builtinNames are the effect, prepare, workspace and workflow step names
// holding a . or _.
func builtinNames() map[string]bool {
	var all []string
	for _, e := range builtin.Effects {
		all = append(all, e.Name)
	}
	for _, r := range builtin.Roles {
		all = append(all, r.Prepare)
	}
	all = append(all, builtin.Names[builtin.SlotWorkspace]...)
	for _, st := range builtin.Steps {
		all = append(all, st.Name)
	}
	names := map[string]bool{}
	for _, n := range all {
		if strings.ContainsAny(n, "._") {
			names[n] = true
		}
	}
	return names
}

// builtinLiterals (R6) flags a string literal equal to a name of builtinNames
// in a non-test file outside builtinTables.
func builtinLiterals(fset *token.FileSet, file string, f *ast.File) []string {
	if testFile(file) || slices.Contains(builtinTables, file) || historicalMigration(file) {
		return nil
	}
	names := builtinNames()
	var out []string
	ast.Inspect(f, func(n ast.Node) bool {
		lit, ok := n.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return true
		}
		if s, err := strconv.Unquote(lit.Value); err == nil && names[s] {
			out = append(out, fmt.Sprintf("%s: %q; a builtin's name lives in its table (%s)", fset.Position(lit.Pos()), s, strings.Join(builtinTables, ", ")))
		}
		return true
	})
	return out
}

// TestBoundaryImports checks that the catalog, the intent vocabulary's leaves,
// the loader and `termfmt` import only their allowed sets.
func TestBoundaryImports(t *testing.T) {
	t.Parallel()
	termfmt := false
	for _, p := range packages(t) {
		termfmt = termfmt || p.ImportPath == module+"internal/termfmt"
		for _, b := range slices.Concat(catalogImports(p.ImportPath, p.Imports), leafImports(p.ImportPath, p.Imports), loaderImports(p.ImportPath, p.Imports), termfmtImports(p.ImportPath, p.Imports)) {
			t.Error(b)
		}
	}
	if !termfmt {
		t.Error("internal/termfmt is missing; R6 would match nothing")
	}
}

// TestOnlyTheCatalogBuildsActions checks that only the catalog builds action
// specs.
func TestOnlyTheCatalogBuildsActions(t *testing.T) {
	t.Parallel()
	fset, files := typedFiles(t)
	for _, tf := range files {
		for _, b := range actionLiterals(fset, tf.rel, tf.info, tf.file) {
			t.Error(b)
		}
	}
}

// TestOnlyTheCatalogEvaluatesIntentPredicates checks that only the loader and
// the catalog read an intent's predicates.
func TestOnlyTheCatalogEvaluatesIntentPredicates(t *testing.T) {
	t.Parallel()
	fset, files := typedFiles(t)
	for _, tf := range files {
		for _, b := range programReads(fset, tf.rel, tf.info, tf.file) {
			t.Error(b)
		}
	}
}

// TestBuiltinNamesLiveInTheirTables checks that builtin names appear only in
// their table files. A released migration keeps the words that version stored,
// so migrate_v*.go files are history and exempt.
func TestBuiltinNamesLiveInTheirTables(t *testing.T) {
	t.Parallel()
	for _, p := range packages(t) {
		self, ok := local(p.ImportPath)
		if !ok {
			continue
		}
		for _, name := range p.GoFiles {
			fset, f := parseFile(t, filepath.Join(p.Dir, name))
			for _, b := range builtinLiterals(fset, self+"/"+name, f) {
				t.Error(b)
			}
		}
	}
}

// TestTheBoundaryAnchorsExist fails when a package, type or field the rules
// name moves, which would leave R4 and R5 matching nothing.
func TestTheBoundaryAnchorsExist(t *testing.T) {
	t.Parallel()
	_, files := typedFiles(t)
	pkgs := map[string]*types.Package{}
	for _, tf := range files {
		pkgs[tf.pkg.Path()] = tf.pkg
	}
	var missing []string
	if pkgs[module+"internal/catalog"] == nil {
		missing = append(missing, "internal/catalog is missing; R1, R4 and R5 guard it")
	}
	for _, name := range actionTypes {
		dot := strings.LastIndex(name, ".")
		if p := pkgs[name[:dot]]; p == nil || p.Scope().Lookup(name[dot+1:]) == nil {
			missing = append(missing, name+" is missing; R4 would match nothing")
		}
	}
	loader := pkgs[module+"internal/intent"]
	if loader == nil || loader.Scope().Lookup("Programs") == nil {
		t.Fatal("internal/intent.Programs is missing; R5 would match nothing")
	}
	for _, name := range programFields {
		found := false
		for _, typ := range []string{"Programs", "ActionPrograms"} {
			if o := loader.Scope().Lookup(typ); o != nil {
				obj, _, _ := types.LookupFieldOrMethod(o.Type(), false, loader, name)
				v, ok := obj.(*types.Var)
				found = found || ok && programField(v)
			}
		}
		if !found {
			missing = append(missing, "Programs."+name+" is missing; R5 would match nothing")
		}
	}
	flagged(t, "the boundary anchors", missing, 0, "")
}
