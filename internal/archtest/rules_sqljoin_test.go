package archtest

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"maps"
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
	"core":   {"muted_until", "mute_until_state", "muted_kind", "muted_waiting_on", "next_guidance", "hold"},
	"plugin": {"taken_over", "tracked"},
}

var (
	sqlTable = regexp.MustCompile(`(?i)\b(?:FROM|JOIN|INTO|UPDATE|TABLE)\s+([a-z_][a-z0-9_]*)`)
	sqlWord  = regexp.MustCompile(`[a-z_][a-z0-9_]*`)
)

// names are what a query's text resolves names by: the package-level
// constants and variables, and the locals of the function at hand.
type names struct {
	decls  map[string]ast.Expr
	locals map[string]string
}

// queryText is the text e builds: its literals, with the names n knows
// resolved, joined by spaces.
func queryText(e ast.Expr, n names, seen map[string]bool) string {
	switch e := e.(type) {
	case *ast.BasicLit:
		if e.Kind != token.STRING {
			return ""
		}
		s, _ := strconv.Unquote(e.Value)
		return s
	case *ast.Ident:
		if l, ok := n.locals[e.Name]; ok {
			return l
		}
		d, ok := n.decls[e.Name]
		if !ok || seen[e.Name] {
			return ""
		}
		seen[e.Name] = true
		defer delete(seen, e.Name)
		return queryText(d, n, seen)
	case *ast.ParenExpr:
		return queryText(e.X, n, seen)
	case *ast.BinaryExpr:
		return queryText(e.X, n, seen) + " " + queryText(e.Y, n, seen)
	case *ast.CallExpr:
		parts := make([]string, len(e.Args))
		for i, a := range e.Args {
			parts[i] = queryText(a, n, seen)
		}
		return strings.Join(parts, " ")
	}
	return ""
}

// sqlPackage is a package's files with what resolves their queries.
type sqlPackage struct {
	fset  *token.FileSet
	files map[string]*ast.File
	decls map[string]ast.Expr
	funcs map[string][]*ast.FuncDecl
}

func newSQLPackage(fset *token.FileSet, files map[string]*ast.File) *sqlPackage {
	p := &sqlPackage{fset: fset, files: files, decls: map[string]ast.Expr{}, funcs: map[string][]*ast.FuncDecl{}}
	for _, f := range files {
		for _, d := range f.Decls {
			switch d := d.(type) {
			case *ast.FuncDecl:
				p.funcs[d.Name.Name] = append(p.funcs[d.Name.Name], d)
			case *ast.GenDecl:
				if d.Tok != token.CONST && d.Tok != token.VAR {
					continue
				}
				for _, spec := range d.Specs {
					v := spec.(*ast.ValueSpec)
					for i, n := range v.Names {
						if i < len(v.Values) {
							p.decls[n.Name] = v.Values[i]
						}
					}
				}
			}
		}
	}
	return p
}

// locals are the texts body's local names build, through :=, = and +=.
func (p *sqlPackage) locals(body *ast.BlockStmt) map[string]string {
	out := map[string]string{}
	ast.Inspect(body, func(n ast.Node) bool {
		a, ok := n.(*ast.AssignStmt)
		if !ok || len(a.Lhs) != len(a.Rhs) {
			return true
		}
		for i, l := range a.Lhs {
			id, ok := l.(*ast.Ident)
			if !ok {
				continue
			}
			text := queryText(a.Rhs[i], names{p.decls, out}, map[string]bool{})
			if a.Tok == token.ADD_ASSIGN {
				out[id.Name] += " " + text
			} else {
				out[id.Name] = text
			}
		}
		return true
	})
	return out
}

// queries are the SQL texts of the package by position: each string
// literal or concatenation that is no part of a larger one, a bare name
// left out since its declaration is a query of its own, and each local a
// function builds.
func (p *sqlPackage) queries() map[string]string {
	out := map[string]string{}
	n := names{decls: p.decls}
	for path, f := range p.files {
		ast.Inspect(f, func(node ast.Node) bool {
			switch e := node.(type) {
			case *ast.FuncDecl:
				if e.Body != nil {
					for name, text := range p.locals(e.Body) {
						out[fmt.Sprintf("%s:%d: %s", path, p.fset.Position(e.Pos()).Line, name)] = text
					}
				}
			case *ast.BinaryExpr, *ast.BasicLit:
				if b, ok := e.(*ast.BinaryExpr); ok && b.Op != token.ADD {
					return true
				}
				out[fmt.Sprintf("%s:%d", path, p.fset.Position(e.Pos()).Line)] += queryText(e.(ast.Expr), n, map[string]bool{}) + " "
				return false
			}
			return true
		})
	}
	return out
}

// bodyText is every query text body holds, its names resolved, those of
// the function around it in outer, with the bodies of the package's
// functions it hands tx to.
func (p *sqlPackage) bodyText(body *ast.BlockStmt, tx string, outer map[string]string, seen map[*ast.FuncDecl]bool) string {
	locals := maps.Clone(outer)
	if locals == nil {
		locals = map[string]string{}
	}
	maps.Copy(locals, p.locals(body))
	n := names{decls: p.decls, locals: locals}
	var b strings.Builder
	ast.Inspect(body, func(node ast.Node) bool {
		switch e := node.(type) {
		case *ast.CallExpr:
			if !slices.ContainsFunc(e.Args, func(a ast.Expr) bool { id, ok := a.(*ast.Ident); return ok && id.Name == tx }) {
				return true
			}
			name := ""
			switch f := e.Fun.(type) {
			case *ast.Ident:
				name = f.Name
			case *ast.SelectorExpr:
				name = f.Sel.Name
			}
			for _, d := range p.funcs[name] {
				if d.Body != nil && !seen[d] {
					seen[d] = true
					b.WriteString(p.bodyText(d.Body, txParam(d.Type), nil, seen) + " ")
				}
			}
		case *ast.BinaryExpr, *ast.BasicLit, *ast.Ident:
			if bin, ok := e.(*ast.BinaryExpr); ok && bin.Op != token.ADD {
				return true
			}
			b.WriteString(queryText(e.(ast.Expr), n, map[string]bool{}) + " ")
			return false
		}
		return true
	})
	return b.String()
}

// txParam is the name of ft's *Tx parameter, "" with none.
func txParam(ft *ast.FuncType) string {
	for _, f := range ft.Params.List {
		star, ok := f.Type.(*ast.StarExpr)
		if !ok {
			continue
		}
		var name string
		switch t := star.X.(type) {
		case *ast.Ident:
			name = t.Name
		case *ast.SelectorExpr:
			name = t.Sel.Name
		}
		if name == "Tx" && len(f.Names) > 0 {
			return f.Names[0].Name
		}
	}
	return ""
}

// transactions are the bodies that run in one transaction, by the
// function they are in: a function literal passed to Write, and a function
// that takes a *Tx; each with its text.
func (p *sqlPackage) transactions() []sqlTx {
	var out []sqlTx
	for path, f := range p.files {
		for _, d := range f.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok || fd.Body == nil {
				continue
			}
			if tx := txParam(fd.Type); tx != "" {
				out = append(out, sqlTx{path, p.fset.Position(fd.Pos()).Line, fd.Name.Name, p.bodyText(fd.Body, tx, nil, map[*ast.FuncDecl]bool{fd: true})})
			}
			outer := p.locals(fd.Body)
			ast.Inspect(fd.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				if sel, ok := call.Fun.(*ast.SelectorExpr); !ok || sel.Sel.Name != "Write" {
					return true
				}
				for _, a := range call.Args {
					if lit, ok := a.(*ast.FuncLit); ok {
						out = append(out, sqlTx{path, p.fset.Position(lit.Pos()).Line, fd.Name.Name, p.bodyText(lit.Body, txParam(lit.Type), outer, map[*ast.FuncDecl]bool{})})
					}
				}
				return true
			})
		}
	}
	return out
}

// sqlTx is one transaction's body: where, in which function, and its text.
type sqlTx struct {
	path string
	line int
	fn   string
	text string
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

// sqlJoinAllowlist are the functions, by file, whose transaction still
// writes a plugin's table and a core's.
var sqlJoinAllowlist = []sqlJoinAllowed{}

type sqlJoinAllowed struct{ file, fn, reason string }

// ruleSQLJoin flags each query of files, a package's by path, that names
// a plugin's table and a core table, and each transaction whose queries
// do, overrides counting by the columns named; used marks the allowlist
// entries that matched.
func ruleSQLJoin(fset *token.FileSet, files map[string]*ast.File, core []string, allow []sqlJoinAllowed, used map[int]bool) []string {
	p := newSQLPackage(fset, files)
	var out []string
	mixed := func(q string) (string, bool) {
		if !sqlText.MatchString(q) {
			return "", false
		}
		o := owners(q, core)
		if len(o["plugin"]) == 0 || len(o["core"]) == 0 {
			return "", false
		}
		slices.Sort(o["plugin"])
		slices.Sort(o["core"])
		return fmt.Sprintf("the plugin's %s and the core's %s", strings.Join(o["plugin"], ", "), strings.Join(o["core"], ", ")), true
	}
	for at, q := range p.queries() {
		if what, ok := mixed(q); ok {
			out = append(out, fmt.Sprintf("%s: a query names %s; plugin storage shares no join or transaction with the core (R-sqljoin)", at, what))
		}
	}
	for _, tx := range p.transactions() {
		what, ok := mixed(tx.text)
		if !ok {
			continue
		}
		if i := slices.IndexFunc(allow, func(a sqlJoinAllowed) bool { return a.file == tx.path && a.fn == tx.fn }); i >= 0 {
			used[i] = true
			continue
		}
		out = append(out, fmt.Sprintf("%s:%d: a transaction of %s names %s; plugin storage shares no join or transaction with the core (R-sqljoin)", tx.path, tx.line, tx.fn, what))
	}
	slices.Sort(out)
	return slices.Compact(out)
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
// `internal/adapter/store`, and no transaction (a function literal passed
// to `Write`, or a function taking a `*Tx`, with the functions it hands the
// transaction to), names both a table a source plugin owns and a core
// table: the plugin tables leave the core store, so a query that joins them
// with the core's becomes a snapshot read or a broker call, and a write of
// both becomes two. The migrations, history of the shared file, are exempt.
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
	used := map[int]bool{}
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
		for _, b := range ruleSQLJoin(fset, files, core, sqlJoinAllowlist, used) {
			t.Error(b)
		}
	}
	for i, a := range sqlJoinAllowlist {
		if !used[i] {
			t.Errorf("allowlist entry %s %s matches no transaction; remove it", a.file, a.fn)
		}
	}
}
