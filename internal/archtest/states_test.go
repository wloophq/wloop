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

func stateLiteralHits(fset *token.FileSet, rel string, info *types.Info, f *ast.File) []string {
	var out []string
	declares := rel == "internal/domain/task/schema.go"
	ast.Inspect(f, func(n ast.Node) bool {
		if d, ok := n.(*ast.GenDecl); ok && declares && (d.Tok == token.CONST || d.Tok == token.VAR) {
			return false
		}
		lit, ok := n.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return true
		}
		typ, ok := stateTyped(info, lit)
		if v, err := strconv.Unquote(lit.Value); ok && err == nil && v != "" {
			out = append(out, fmt.Sprintf("%s:%d: the literal %s is a task.%s; read the category, bind the state from the profile",
				rel, fset.Position(lit.Pos()).Line, lit.Value, typ))
		}
		return true
	})
	return out
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
