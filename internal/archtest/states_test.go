package archtest

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
)

const taskPkg = module + "internal/domain/task"

// stateTypes are the types whose values name a state or a category.
var stateTypes = []string{taskPkg + ".State", taskPkg + ".Category"}

// stateExempt says whether a file may spell a state or a category: the profile's loader, which reads them,
// and the version 7 migration, which carries the states of version 6 as history.
func stateExempt(rel string) bool {
	return under(rel, "internal/intent") || strings.HasPrefix(filepath.Base(rel), "migrate_v7")
}

// TestGoNamesNoState checks that Go reads a task's category and never names a state: no Go literal is
// converted to, assigned to or compared with a task.State or a task.Category outside internal/intent and the
// version 7 migration, but the Category constants and Categories of internal/domain/task/schema.go, which
// declare the closed set; and no SQL text under internal/adapter/store compares the state column with a state
// the bundled profile declares, nor inlines a category, which it binds as a parameter.
func TestGoNamesNoState(t *testing.T) {
	t.Parallel()
	fset, files := typedFiles(t)
	var hits []string
	for _, tf := range files {
		if stateExempt(tf.rel) {
			continue
		}
		hits = append(hits, stateLiteralHits(fset, tf.rel, tf.info, tf.file)...)
	}
	states := bundledStates(t)
	forEachFile(t, false, func(fset *token.FileSet, file string, f *ast.File) {
		if under(file, "internal/adapter/store") && !stateExempt(file) {
			hits = append(hits, stateSQLHits(fset, file, f, states)...)
		}
	})
	slices.Sort(hits)
	for _, h := range hits {
		t.Error(h)
	}
}

// bundledStates are the states the bundled tasks.toml declares.
func bundledStates(t *testing.T) []string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "profiles", "default", "tasks.toml"))
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, m := range regexp.MustCompile(`(?m)^\[states\.([\w-]+)\]`).FindAllStringSubmatch(string(b), -1) {
		out = append(out, m[1])
	}
	if len(out) == 0 {
		t.Fatal("the bundled tasks.toml declares no state")
	}
	return out
}

func stateTyped(info *types.Info, e ast.Expr) (string, bool) {
	tv, ok := info.Types[e]
	if !ok {
		return "", false
	}
	n, ok := types.Unalias(tv.Type).(*types.Named)
	if !ok || n.Obj().Pkg() == nil {
		return "", false
	}
	name := n.Obj().Pkg().Path() + "." + n.Obj().Name()
	return n.Obj().Name(), slices.Contains(stateTypes, name)
}

// stateLiteralHits are the constant expressions of f typed task.State or task.Category: a literal, a named
// constant or a conversion of one. The Category constants of internal/domain/task, the closed set, may be
// read anywhere, and declared, with Categories, only in schema.go.
func stateLiteralHits(fset *token.FileSet, rel string, info *types.Info, f *ast.File) []string {
	var out []string
	declares := rel == "internal/domain/task/schema.go"
	ast.Inspect(f, func(n ast.Node) bool {
		if d, ok := n.(*ast.GenDecl); ok && declares && closedSetDecl(info, d) {
			return false
		}
		e, ok := n.(ast.Expr)
		if !ok {
			return true
		}
		tv, ok := info.Types[e]
		if !ok || tv.Value == nil {
			return true
		}
		typ, ok := stateTyped(info, e)
		if !ok || tv.Value.ExactString() == `""` || closedSetConst(info, e) {
			return true
		}
		out = append(out, fmt.Sprintf("%s:%d: %s is a task.%s constant; read the category, bind the state from the profile",
			rel, fset.Position(e.Pos()).Line, tv.Value.ExactString(), typ))
		return false
	})
	return out
}

// closedSetConst says whether e names a Category constant of internal/domain/task.
func closedSetConst(info *types.Info, e ast.Expr) bool {
	var id *ast.Ident
	switch x := ast.Unparen(e).(type) {
	case *ast.Ident:
		id = x
	case *ast.SelectorExpr:
		id = x.Sel
	default:
		return false
	}
	c, ok := info.Uses[id].(*types.Const)
	return ok && c.Pkg() != nil && c.Pkg().Path() == taskPkg && types.TypeString(c.Type(), nil) == taskPkg+".Category"
}

// closedSetDecl says whether d declares the closed set: a const block of task.Category constants, or the
// Categories var.
func closedSetDecl(info *types.Info, d *ast.GenDecl) bool {
	if len(d.Specs) == 0 {
		return false
	}
	for _, sp := range d.Specs {
		vs, ok := sp.(*ast.ValueSpec)
		if !ok {
			return false
		}
		for _, name := range vs.Names {
			obj := info.Defs[name]
			switch {
			case obj == nil:
				return false
			case d.Tok == token.CONST && types.TypeString(obj.Type(), nil) == taskPkg+".Category":
			case d.Tok == token.VAR && name.Name == "Categories":
			default:
				return false
			}
		}
	}
	return true
}

var (
	stateCompare    = regexp.MustCompile(`(?i)\bstate\s*(?:=|==|<>|!=|NOT\s+IN|IN)\s*\(?\s*((?:'[^']*'\s*,?\s*)+)`)
	categoryCompare = regexp.MustCompile(`(?i)\bcategory\s*(?:=|==|<>|!=|NOT\s+IN|IN)\s*\(?\s*'`)
	quoted          = regexp.MustCompile(`'([^']*)'`)
)

// stateSQLHits are f's SQL texts that compare the state column with one of states or inline a category.
func stateSQLHits(fset *token.FileSet, rel string, f *ast.File, states []string) []string {
	var out []string
	ast.Inspect(f, func(n ast.Node) bool {
		lit, ok := n.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return true
		}
		text, err := strconv.Unquote(lit.Value)
		if err != nil {
			return true
		}
		at := fmt.Sprintf("%s:%d: ", rel, fset.Position(lit.Pos()).Line)
		for _, m := range stateCompare.FindAllStringSubmatch(text, -1) {
			for _, q := range quoted.FindAllStringSubmatch(m[1], -1) {
				if slices.Contains(states, q[1]) {
					out = append(out, at+fmt.Sprintf("SQL compares the state column with the state %q; filter by category", q[1]))
				}
			}
		}
		if categoryCompare.MatchString(text) {
			out = append(out, at+"SQL inlines a category; bind it as ? from the task.Category constants")
		}
		return true
	})
	return out
}

// TestGoNamesNoBundledProperty checks that Go names no property the bundled tasks.toml gives a role (area,
// contexts, star, deadline): outside internal/intent, no Go call of Prop, SetProp, PropText, PropList or
// PropTime passes such a name as a constant. Go names the role and reads the property the profile gives it.
func TestGoNamesNoBundledProperty(t *testing.T) {
	t.Parallel()
	props := roledProps(t)
	fset, files := typedFiles(t)
	var hits []string
	for _, tf := range files {
		if !stateExempt(tf.rel) {
			hits = append(hits, propNameHits(fset, tf.rel, tf.info, tf.file, props)...)
		}
	}
	slices.Sort(hits)
	for _, h := range hits {
		t.Error(h)
	}
}

// roledProps are the properties the bundled tasks.toml gives a role.
func roledProps(t *testing.T) []string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "profiles", "default", "tasks.toml"))
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, m := range regexp.MustCompile(`(?m)^\[props\.([\w-]+)\]\n(?:[^\[\n].*\n)*?role\s*=`).FindAllStringSubmatch(string(b), -1) {
		out = append(out, m[1])
	}
	if len(out) == 0 {
		t.Fatal("the bundled tasks.toml gives no property a role")
	}
	return out
}

// propAccessors are the methods that read or write a property by its name.
var propAccessors = []string{"Prop", "SetProp", "PropText", "PropList", "PropTime"}

// propNameHits are the calls of f to a property accessor whose first argument is a constant naming one of props.
func propNameHits(fset *token.FileSet, rel string, info *types.Info, f *ast.File, props []string) []string {
	var out []string
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || len(call.Args) == 0 {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || !slices.Contains(propAccessors, sel.Sel.Name) {
			return true
		}
		tv, ok := info.Types[call.Args[0]]
		if !ok || tv.Value == nil {
			return true
		}
		name, err := strconv.Unquote(tv.Value.ExactString())
		if err == nil && slices.Contains(props, name) {
			out = append(out, fmt.Sprintf("%s:%d: %s names the property %q; read the property of its role", rel, fset.Position(call.Pos()).Line, sel.Sel.Name, name))
		}
		return true
	})
	return out
}
