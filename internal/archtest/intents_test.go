package archtest

import (
	"encoding/json"
	"flag"
	"fmt"
	"go/ast"
	"go/constant"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
)

var report = flag.Bool("report", false, "list TestNoIntentEnumerations' hits instead of failing")

// intentTypes are the types whose values name an intent.
var intentTypes = []string{
	module + "internal/domain/intent.Name",
	module + "internal/domain/turn.Intent",
	module + "internal/turn.Intent",
	module + "internal/profile.Kind",
}

var intentConsts = []string{"Review", "Address", "Nudge", "Probe", "Act"}

// intentAllowlist are the files that may name the intents Go binds by name.
var intentAllowlist = map[string]string{
	"internal/core/turns/turnstest/store.go":       "test fixtures of today's intents",
	"internal/core/turns/turnstest/intents.go":     "the test registry of today's intents",
	"internal/core/intake/intaketest/scenarios.go": "test fixtures of today's intents",
	"internal/turn/scenarios.go":                   "eval fixtures",
}

// exempt are where the intent names are declared.
func exempt(rel string) bool {
	return under(rel, "internal/domain/intent", "profiles")
}

type exportPkg struct {
	ImportPath string
	Dir        string
	GoFiles    []string
	Export     string
	Standard   bool
	Module     *struct{ Main bool }
}

// TestNoIntentEnumerations checks that Go compares, switches on or lists no
// intent name outside allowlisted files.
func TestNoIntentEnumerations(t *testing.T) {
	t.Parallel()
	fset, files := typedFiles(t)
	var hits []string
	used := map[string]bool{}
	for _, tf := range files {
		if exempt(tf.rel) {
			continue
		}
		for _, h := range enumerations(fset, tf.info, tf.file) {
			hits = append(hits, fmt.Sprintf("%s:%s", tf.rel, h))
		}
		for _, pos := range constUses(tf.info, tf.file) {
			if _, ok := intentAllowlist[tf.rel]; ok {
				used[tf.rel] = true
				continue
			}
			hits = append(hits, fmt.Sprintf("%s:%d: names an intent constant outside the allowlist", tf.rel, fset.Position(pos).Line))
		}
	}
	for rel := range intentAllowlist {
		if !used[rel] {
			hits = append(hits, rel+": allowlisted but names no intent constant; drop it from intentAllowlist")
		}
	}
	slices.Sort(hits)
	for _, h := range hits {
		if *report {
			t.Log(h)
		} else {
			t.Error(h)
		}
	}
}

// typedFile is a non-test Go file of this module, type-checked.
type typedFile struct {
	rel  string // slash path from the module root
	file *ast.File
	info *types.Info
	pkg  *types.Package
}

var typed struct {
	once  sync.Once
	fset  *token.FileSet
	files []typedFile
	err   error
}

// typedFiles type-checks the module's non-test files once per test binary.
func typedFiles(t *testing.T) (*token.FileSet, []typedFile) {
	t.Helper()
	typed.once.Do(func() { typed.fset, typed.files, typed.err = typeCheck() })
	if typed.err != nil {
		t.Fatal(typed.err)
	}
	return typed.fset, typed.files
}

func typeCheck() (*token.FileSet, []typedFile, error) {
	pkgs, err := exportPackages()
	if err != nil {
		return nil, nil, err
	}
	exports := map[string]string{}
	for _, p := range pkgs {
		exports[p.ImportPath] = p.Export
	}
	fset := fileSet
	imp := importer.ForCompiler(fset, "gc", func(path string) (io.ReadCloser, error) {
		f, ok := exports[path]
		if !ok || f == "" {
			return nil, fmt.Errorf("no export data for %s", path)
		}
		return os.Open(f)
	})
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		return nil, nil, err
	}
	var out []typedFile
	for _, p := range pkgs {
		self, ok := local(p.ImportPath)
		if !ok || p.Module == nil || !p.Module.Main {
			continue
		}
		var files []*ast.File
		for _, name := range p.GoFiles {
			f, err := parseOnce(filepath.Join(p.Dir, name))
			if err != nil {
				return nil, nil, err
			}
			files = append(files, f)
		}
		info := &types.Info{Types: map[ast.Expr]types.TypeAndValue{}, Uses: map[*ast.Ident]types.Object{}, Defs: map[*ast.Ident]types.Object{}, Selections: map[*ast.SelectorExpr]*types.Selection{}}
		pkg, err := (&types.Config{Importer: imp}).Check(p.ImportPath, fset, files, info)
		if err != nil {
			return nil, nil, fmt.Errorf("%s: %w", self, err)
		}
		for _, f := range files {
			rel, err := filepath.Rel(root, fset.Position(f.Pos()).Filename)
			if err != nil {
				return nil, nil, err
			}
			out = append(out, typedFile{rel: filepath.ToSlash(rel), file: f, info: info, pkg: pkg})
		}
	}
	return fset, out, nil
}

func exportPackages() ([]exportPkg, error) {
	cmd := exec.Command("go", "list", "-deps", "-export", "-json=ImportPath,Dir,GoFiles,Export,Standard,Module", "./...")
	cmd.Dir = filepath.Join("..", "..")
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	var all []exportPkg
	dec := json.NewDecoder(strings.NewReader(string(out)))
	for dec.More() {
		var p exportPkg
		if err := dec.Decode(&p); err != nil {
			return nil, err
		}
		all = append(all, p)
	}
	return all, nil
}

func intentTyped(info *types.Info, e ast.Expr) bool {
	tv, ok := info.Types[e]
	if !ok {
		return false
	}
	n, ok := types.Unalias(tv.Type).(*types.Named)
	if !ok || n.Obj().Pkg() == nil {
		return false
	}
	return slices.Contains(intentTypes, n.Obj().Pkg().Path()+"."+n.Obj().Name())
}

// literal is the non-empty string literal or string constant e is, unwrapping parentheses and
// conversions such as dintent.Name("review").
func literal(info *types.Info, e ast.Expr) (string, bool) {
	e = ast.Unparen(e)
	if c, ok := e.(*ast.CallExpr); ok && len(c.Args) == 1 && info.Types[c.Fun].IsType() {
		return literal(info, c.Args[0])
	}
	if b, ok := e.(*ast.BasicLit); ok && b.Kind == token.STRING {
		s, err := strconv.Unquote(b.Value)
		return s, err == nil && s != ""
	}
	if tv, ok := info.Types[e]; ok && tv.Value != nil && tv.Value.Kind() == constant.String && !isIntentConst(info, e) {
		s := constant.StringVal(tv.Value)
		return s, s != ""
	}
	return "", false
}

// isIntentConst is one of the intent constants, which constUses accounts for.
func isIntentConst(info *types.Info, e ast.Expr) bool {
	id, ok := e.(*ast.Ident)
	if s, sel := e.(*ast.SelectorExpr); sel {
		id, ok = s.Sel, true
	}
	if !ok {
		return false
	}
	c, ok := info.Uses[id].(*types.Const)
	return ok && c.Pkg() != nil && c.Pkg().Path() == module+"internal/domain/intent" && slices.Contains(intentConsts, c.Name())
}

// intentValued is a value of an intent type, or a conversion of one.
func intentValued(info *types.Info, e ast.Expr) bool {
	e = ast.Unparen(e)
	if intentTyped(info, e) {
		if _, lit := literal(info, e); !lit {
			return true
		}
	}
	if c, ok := e.(*ast.CallExpr); ok && len(c.Args) == 1 && info.Types[c.Fun].IsType() {
		return intentValued(info, c.Args[0])
	}
	return false
}

func namedIntent(e ast.Expr) bool {
	s, ok := ast.Unparen(e).(*ast.SelectorExpr)
	return ok && s.Sel.Name == "Intent"
}

func enumerations(fset *token.FileSet, info *types.Info, f *ast.File) []string {
	var out []string
	hit := func(n ast.Node, format string, args ...any) {
		out = append(out, fmt.Sprintf("%d: ", fset.Position(n.Pos()).Line)+fmt.Sprintf(format, args...))
	}
	ast.Inspect(f, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.BasicLit:
			if s, ok := literal(info, n); ok && strings.Contains(s, "review, address") {
				hit(n, "a string enumerates the intents: %q", s)
			}
		case *ast.BinaryExpr:
			if n.Op != token.EQL && n.Op != token.NEQ {
				return true
			}
			for _, pair := range [][2]ast.Expr{{n.X, n.Y}, {n.Y, n.X}} {
				s, ok := literal(info, pair[0])
				if !ok {
					continue
				}
				switch {
				case intentTyped(info, pair[1]) || intentValued(info, pair[1]):
					hit(n, "compares an intent with %q", s)
				case namedIntent(pair[1]):
					hit(n, "compares a field named Intent with %q", s)
				}
			}
		case *ast.SwitchStmt:
			if n.Tag == nil || !(intentTyped(info, n.Tag) || intentValued(info, n.Tag) || namedIntent(n.Tag)) {
				return true
			}
			for _, c := range n.Body.List {
				for _, e := range c.(*ast.CaseClause).List {
					if s, ok := literal(info, e); ok {
						hit(e, "switches an intent on %q", s)
					}
				}
			}
		case *ast.CompositeLit:
			tv, ok := info.Types[n]
			if !ok {
				return true
			}
			var elemTyped bool
			switch u := tv.Type.Underlying().(type) {
			case *types.Slice:
				elemTyped = isIntentType(u.Elem())
			case *types.Array:
				elemTyped = isIntentType(u.Elem())
			case *types.Map:
				elemTyped = isIntentType(u.Key()) || isIntentType(u.Elem())
			default:
				return true
			}
			var lits []ast.Expr
			for _, el := range n.Elts {
				parts := []ast.Expr{el}
				if kv, ok := el.(*ast.KeyValueExpr); ok {
					parts = []ast.Expr{kv.Key, kv.Value}
				}
				for _, p := range parts {
					if _, ok := literal(info, p); ok {
						lits = append(lits, p)
					} else if intentValued(info, p) {
						elemTyped = true
					}
				}
			}
			if elemTyped {
				for _, l := range lits {
					s, _ := literal(info, l)
					hit(l, "lists %q with intents", s)
				}
			}
		}
		return true
	})
	return out
}

func isIntentType(t types.Type) bool {
	n, ok := types.Unalias(t).(*types.Named)
	return ok && n.Obj().Pkg() != nil && slices.Contains(intentTypes, n.Obj().Pkg().Path()+"."+n.Obj().Name())
}

func constUses(info *types.Info, f *ast.File) []token.Pos {
	var out []token.Pos
	ast.Inspect(f, func(n ast.Node) bool {
		id, ok := n.(*ast.Ident)
		if !ok {
			return true
		}
		c, ok := info.Uses[id].(*types.Const)
		if ok && c.Pkg() != nil && c.Pkg().Path() == module+"internal/domain/intent" && slices.Contains(intentConsts, c.Name()) {
			out = append(out, id.Pos())
		}
		return true
	})
	return out
}

func TestAnIntentComparedWithANamedConstantIsCaught(t *testing.T) {
	t.Parallel()
	const src = `package intent

type Name string

const Review Name = "review"
const reviewName = "review"

func f(n Name) (bool, bool) { return n == reviewName, n == Review }
`
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "plant.go", src, 0)
	if err != nil {
		t.Fatal(err)
	}
	info := &types.Info{Types: map[ast.Expr]types.TypeAndValue{}, Uses: map[*ast.Ident]types.Object{}}
	if _, err := (&types.Config{}).Check(module+"internal/domain/intent", fset, []*ast.File{f}, info); err != nil {
		t.Fatal(err)
	}
	flagged(t, "the named constant", enumerations(fset, info, f), 1, "8: ")
}
