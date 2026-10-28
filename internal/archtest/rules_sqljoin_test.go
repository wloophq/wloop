package archtest

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// pluginTables are the tables of the source plugins that still sit in the
// core store's file; a name ending in _ is a prefix.
var pluginTables = []string{"pull_requests", "pr_seen", "pr_details", "fetches", "segment_map", "slack_", "extraction_"}

// sharedTables hold both the core's rows and the plugins' (meta) or both
// columns (overrides), so they count by the columns a query names.
var sharedTables = map[string]bool{"meta": true, "overrides": true}

// overrideColumns are the columns of overrides by owner.
var overrideColumns = map[string][]string{
	"core":   {"muted_until", "mute_until_state", "muted_kind", "muted_waiting_on", "next_guidance"},
	"plugin": {"hold", "taken_over", "tracked"},
}

var (
	sqlTable = regexp.MustCompile(`(?i)\b(?:FROM|JOIN|INTO|UPDATE|TABLE)\s+([a-z_][a-z0-9_]*)`)
	sqlWord  = regexp.MustCompile(`[a-z_][a-z0-9_]*`)
)

// queryText is the text e builds: its literals, with the package-level
// constants and variables decls names resolved, joined by spaces.
func queryText(e ast.Expr, decls map[string]ast.Expr, seen map[string]bool) string {
	switch e := e.(type) {
	case *ast.BasicLit:
		if e.Kind != token.STRING {
			return ""
		}
		s, _ := strconv.Unquote(e.Value)
		return s
	case *ast.Ident:
		d, ok := decls[e.Name]
		if !ok || seen[e.Name] {
			return ""
		}
		seen[e.Name] = true
		defer delete(seen, e.Name)
		return queryText(d, decls, seen)
	case *ast.ParenExpr:
		return queryText(e.X, decls, seen)
	case *ast.BinaryExpr:
		return queryText(e.X, decls, seen) + " " + queryText(e.Y, decls, seen)
	case *ast.CallExpr:
		parts := make([]string, len(e.Args))
		for i, a := range e.Args {
			parts[i] = queryText(a, decls, seen)
		}
		return strings.Join(parts, " ")
	}
	return ""
}

// queries are the SQL texts of files by position: each string literal or
// concatenation that is no part of a larger one, a bare name left out
// since its declaration is a query of its own.
func queries(fset *token.FileSet, files map[string]*ast.File) map[string]string {
	decls := map[string]ast.Expr{}
	for _, f := range files {
		for _, d := range f.Decls {
			g, ok := d.(*ast.GenDecl)
			if !ok || g.Tok != token.CONST && g.Tok != token.VAR {
				continue
			}
			for _, spec := range g.Specs {
				v := spec.(*ast.ValueSpec)
				for i, n := range v.Names {
					if i < len(v.Values) {
						decls[n.Name] = v.Values[i]
					}
				}
			}
		}
	}
	out := map[string]string{}
	for path, f := range files {
		ast.Inspect(f, func(n ast.Node) bool {
			switch e := n.(type) {
			case *ast.BinaryExpr, *ast.BasicLit:
				if b, ok := e.(*ast.BinaryExpr); ok && b.Op != token.ADD {
					return true
				}
				out[fmt.Sprintf("%s:%d", path, fset.Position(e.Pos()).Line)] += queryText(e.(ast.Expr), decls, map[string]bool{}) + " "
				return false
			}
			return true
		})
	}
	return out
}

// owners are the owners of the tables query reads or writes: "core",
// "plugin", or both.
func owners(query string, core []string) map[string][]string {
	words := map[string]bool{}
	for _, w := range sqlWord.FindAllString(query, -1) {
		words[w] = true
	}
	named := map[string]bool{}
	for _, m := range sqlTable.FindAllStringSubmatch(query, -1) {
		named[m[1]] = true
	}
	out := map[string][]string{}
	tables := 0
	for w := range named {
		switch {
		case slices.ContainsFunc(pluginTables, func(p string) bool {
			return w == p || strings.HasSuffix(p, "_") && strings.HasPrefix(w, p)
		}):
			out["plugin"] = append(out["plugin"], w)
			tables++
		case slices.Contains(core, w):
			out["core"] = append(out["core"], w)
			tables++
		case sharedTables[w]:
			tables++
		}
	}
	if named["overrides"] && tables > 1 {
		for owner, cols := range overrideColumns {
			if slices.ContainsFunc(cols, func(c string) bool { return words[c] }) {
				out[owner] = append(out[owner], "overrides")
			}
		}
	}
	return out
}

// ruleSQLJoin flags each query of files, a package's by path, that names
// a plugin's table and a core table, overrides counting by the columns it
// names.
func ruleSQLJoin(fset *token.FileSet, files map[string]*ast.File, core []string) []string {
	var out []string
	for at, q := range queries(fset, files) {
		if !sqlText.MatchString(q) {
			continue
		}
		o := owners(q, core)
		if len(o["plugin"]) == 0 || len(o["core"]) == 0 {
			continue
		}
		slices.Sort(o["plugin"])
		slices.Sort(o["core"])
		out = append(out, fmt.Sprintf("%s: a query names the plugin's %s and the core's %s; plugin storage shares no join or transaction with the core (R-sqljoin)",
			at, strings.Join(o["plugin"], ", "), strings.Join(o["core"], ", ")))
	}
	slices.Sort(out)
	return out
}

// coreTables are the tables schema.sql creates that are neither a
// plugin's nor shared.
func coreTables(t *testing.T, schema string) []string {
	t.Helper()
	raw, err := os.ReadFile(schema)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, m := range regexp.MustCompile(`CREATE TABLE (\w+)`).FindAllStringSubmatch(string(raw), -1) {
		name := m[1]
		if sharedTables[name] || slices.ContainsFunc(pluginTables, func(p string) bool {
			return name == p || strings.HasSuffix(p, "_") && strings.HasPrefix(name, p)
		}) {
			continue
		}
		out = append(out, name)
	}
	return out
}

// TestNoQueryJoinsPluginAndCoreTables checks that no query of
// `internal/adapter/store` names both a table a source plugin owns and a
// core table: the plugin tables leave the core store, so a query that joins
// them with the core's becomes a snapshot read or a broker call. The
// migrations, history of the shared file, are exempt.
func TestNoQueryJoinsPluginAndCoreTables(t *testing.T) {
	t.Parallel()
	seen := false
	var core []string
	for _, p := range packages(t) {
		self, ok := local(p.ImportPath)
		if !ok || !under(self, "internal/adapter/store") {
			continue
		}
		if self == "internal/adapter/store/sqlite" {
			seen = true
			core = coreTables(t, filepath.Join(p.Dir, "schema.sql"))
		}
	}
	if !seen {
		t.Fatal("internal/adapter/store/sqlite is missing from the package list")
	}
	for _, p := range packages(t) {
		self, ok := local(p.ImportPath)
		if !ok || !under(self, "internal/adapter/store") {
			continue
		}
		fset := token.NewFileSet()
		files := map[string]*ast.File{}
		for _, name := range p.GoFiles {
			if strings.HasPrefix(name, "migrate") {
				continue
			}
			f, err := parser.ParseFile(fset, filepath.Join(p.Dir, name), nil, 0)
			if err != nil {
				t.Fatal(err)
			}
			files[self+"/"+name] = f
		}
		for _, b := range ruleSQLJoin(fset, files, core) {
			t.Error(b)
		}
	}
}
