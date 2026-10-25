package archtest

import (
	"fmt"
	"go/ast"
	"go/token"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// ruleR7 flags an import of a package under internal/core beyond the standard
// library, internal/core, internal/domain and internal/meter, or of a standard
// package that reaches processes, the network or the database.
func ruleR7(self string, imports []string) []string {
	if !under(self, "internal/core") {
		return nil
	}
	var out []string
	for _, imp := range imports {
		dep, internal := local(imp)
		if slices.Contains(forbidden, imp) || (!std(imp) && !(internal && under(dep, "internal/domain", "internal/core", "internal/meter"))) {
			out = append(out, fmt.Sprintf("%s imports %s; the core imports only the standard library, the domain, its ports and the meter (R7)", self, imp))
		}
	}
	return out
}

// TestCoreImports checks that the core imports only the standard library, the
// core, the domain and the meter, and nothing that reaches processes, the
// network or the database.
func TestCoreImports(t *testing.T) {
	t.Parallel()
	seen := false
	for _, p := range packages(t) {
		self, ok := local(p.ImportPath)
		if !ok {
			continue
		}
		seen = seen || under(self, "internal/core")
		for _, b := range ruleR7(self, p.Imports) {
			t.Error(b)
		}
	}
	if !seen {
		t.Error("internal/core is missing from the package list")
	}
}

var (
	orcaSpawners = []string{"Command", "CommandContext", "LookPath", "run", "try", "execStdout", "execSh", "Sh", "Stdout"}
	orcaRuntime  = regexp.MustCompile(`(?i)orca-runtime|orca[^"]*\.sock`)
)

func orcaBinary(s string) bool { return path.Base(s) == "orca" }

func calleeName(call *ast.CallExpr) string {
	switch fn := call.Fun.(type) {
	case *ast.Ident:
		return fn.Name
	case *ast.SelectorExpr:
		return fn.Sel.Name
	}
	return ""
}

// managerBinary says whether e reads a Binary field, as a workspace manager
// entry's; a call of a Binary method is no read.
func managerBinary(e ast.Expr) bool {
	found := false
	ast.Inspect(e, func(n ast.Node) bool {
		if call, ok := n.(*ast.CallExpr); ok {
			if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
				found = found || managerBinary(sel.X)
				for _, a := range call.Args {
					found = found || managerBinary(a)
				}
				return false
			}
		}
		if sel, ok := n.(*ast.SelectorExpr); ok && sel.Sel.Name == "Binary" {
			found = true
		}
		return !found
	})
	return found
}

// ruleR9 flags, outside internal/workspace/orca, a call that spawns the orca
// binary with a literal name or through a manager entry's Binary field, and a
// literal naming Orca's runtime file or socket.
func ruleR9(fset *token.FileSet, file string, f *ast.File) []string {
	if under(path.Dir(file), "internal/workspace/orca") {
		return nil
	}
	imports := map[*ast.BasicLit]bool{}
	for _, imp := range f.Imports {
		imports[imp.Path] = true
	}
	var out []string
	ast.Inspect(f, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.CallExpr:
			if !slices.Contains(orcaSpawners, calleeName(n)) {
				return true
			}
			for _, a := range n.Args {
				if managerBinary(a) {
					out = append(out, fmt.Sprintf("%s: runs a workspace manager's Binary; only internal/workspace/orca runs orca (R9)", fset.Position(a.Pos())))
					continue
				}
				if lit, ok := a.(*ast.BasicLit); ok && lit.Kind == token.STRING {
					if s, err := strconv.Unquote(lit.Value); err == nil && orcaBinary(s) {
						out = append(out, fmt.Sprintf("%s: runs %q; only internal/workspace/orca runs orca (R9)", fset.Position(lit.Pos()), s))
					}
				}
			}
		case *ast.BasicLit:
			if n.Kind != token.STRING || imports[n] {
				return true
			}
			if s, err := strconv.Unquote(n.Value); err == nil && orcaRuntime.MatchString(s) {
				out = append(out, fmt.Sprintf("%s: %q names Orca's runtime; only internal/workspace/orca dials its socket (R9)", fset.Position(n.Pos()), s))
			}
		}
		return true
	})
	return out
}

// TestOnlyOrcaAdapterReachesOrca checks that only the Orca manager's package
// reaches Orca's control surface.
func TestOnlyOrcaAdapterReachesOrca(t *testing.T) {
	t.Parallel()
	forEachFile(t, false, func(fset *token.FileSet, file string, f *ast.File) {
		for _, b := range ruleR9(fset, file, f) {
			t.Error(b)
		}
	})
}

var desktopControl = regexp.MustCompile(`(?i)claude://|cc-socks|\.claude/sessions|peer[._-]?keys?\b`)

// ruleR9Desktop flags, outside the Claude Desktop manager's packages, a literal
// naming Desktop's control surface: its claude:// URLs, its session registry,
// and the peer sockets and keys under /tmp/cc-socks.
func ruleR9Desktop(fset *token.FileSet, file string, f *ast.File) []string {
	if under(path.Dir(file), "internal/workspace/claudedesktop", "internal/adapter/claudedesktop") {
		return nil
	}
	imports := map[*ast.BasicLit]bool{}
	for _, imp := range f.Imports {
		imports[imp.Path] = true
	}
	var out []string
	ast.Inspect(f, func(n ast.Node) bool {
		lit, ok := n.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING || imports[lit] {
			return true
		}
		if s, err := strconv.Unquote(lit.Value); err == nil && desktopControl.MatchString(s) {
			out = append(out, fmt.Sprintf("%s: %q names Claude Desktop's control surface; only internal/workspace/claudedesktop and internal/adapter/claudedesktop reach it (R9)", fset.Position(lit.Pos()), s))
		}
		return true
	})
	return out
}

// TestOnlyClaudeDesktopManagerReachesDesktop checks that only the Claude
// Desktop manager's package reaches Claude Desktop's control surface.
func TestOnlyClaudeDesktopManagerReachesDesktop(t *testing.T) {
	t.Parallel()
	forEachFile(t, false, func(fset *token.FileSet, file string, f *ast.File) {
		for _, b := range ruleR9Desktop(fset, file, f) {
			t.Error(b)
		}
	})
}

// ruleR11 flags an import of an adapter, a workspace manager or
// internal/github from internal/domain.
func ruleR11(self string, imports []string) []string {
	if !under(self, "internal/domain") {
		return nil
	}
	var out []string
	for _, imp := range imports {
		if dep, internal := local(imp); internal && under(dep, "internal/adapter", "internal/workspace", "internal/github") {
			out = append(out, fmt.Sprintf("%s imports %s; the domain imports no adapter and no internal/github (R11)", self, imp))
		}
	}
	return out
}

// TestDomainImports checks that the domain imports no adapter, manager or
// `internal/github`.
func TestDomainImports(t *testing.T) {
	t.Parallel()
	seen := false
	for _, p := range packages(t) {
		self, ok := local(p.ImportPath)
		if !ok {
			continue
		}
		seen = seen || under(self, "internal/domain")
		for _, b := range ruleR11(self, p.Imports) {
			t.Error(b)
		}
	}
	if !seen {
		t.Error("internal/domain is missing from the package list")
	}
}

var sqlText = regexp.MustCompile(`(?s)\bSELECT\b.+\bFROM\b|(?i:\bINSERT\s+(OR\s+\w+\s+)?INTO\b|\bUPDATE\s+\w+\s+SET\b|\bDELETE\s+FROM\b|\bCREATE\s+(UNIQUE\s+)?(TABLE|INDEX|VIEW|TRIGGER)\b|\bDROP\s+(TABLE|INDEX|VIEW|TRIGGER)\b|\bALTER\s+TABLE\b|^\s*(PRAGMA|VACUUM)\b)`)

// sqlAllowlist is the SQL text a file outside the store may still hold.
var sqlAllowlist []allowed

// ruleR13 flags a call of sql.Open or sql.OpenDB outside internal/adapter/store/sqlite
// and, in a non-test file outside internal/adapter/store, SQL text in a string
// literal or in the literals of a + chain, where an operand that is no literal
// stands for a gap; used marks the allowlist entries that matched.
func ruleR13(fset *token.FileSet, file string, f *ast.File, allow []allowed, used map[int]bool) []string {
	dir := path.Dir(file)
	var out []string
	if !under(dir, "internal/adapter/store/sqlite") {
		sqlName := ""
		for _, imp := range f.Imports {
			if p, _ := strconv.Unquote(imp.Path.Value); p == "database/sql" {
				sqlName = "sql"
				if imp.Name != nil {
					sqlName = imp.Name.Name
				}
			}
		}
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || sqlName == "" {
				return true
			}
			if sel, ok := call.Fun.(*ast.SelectorExpr); ok && (sel.Sel.Name == "Open" || sel.Sel.Name == "OpenDB") {
				if x, ok := sel.X.(*ast.Ident); ok && x.Name == sqlName {
					out = append(out, fmt.Sprintf("%s: calls sql.%s; only internal/adapter/store/sqlite opens the database (R13)", fset.Position(call.Pos()), sel.Sel.Name))
				}
			}
			return true
		})
	}
	if testFile(file) || under(dir, "internal/adapter/store") {
		return out
	}
	ast.Inspect(f, func(n ast.Node) bool {
		var text string
		switch n := n.(type) {
		case *ast.BinaryExpr:
			if n.Op != token.ADD {
				return true
			}
			var literals int
			text = concatenated(n, &literals)
			if literals == 0 {
				return true
			}
		case *ast.BasicLit:
			if n.Kind != token.STRING {
				return true
			}
			text, _ = strconv.Unquote(n.Value)
		default:
			return true
		}
		if sqlText.MatchString(text) {
			allowedHere := false
			for i, a := range allow {
				if a.file == file && strings.Contains(text, a.literal) {
					used[i] = true
					allowedHere = true
				}
			}
			if !allowedHere {
				out = append(out, fmt.Sprintf("%s: SQL text outside internal/adapter/store (R13)", fset.Position(n.Pos())))
			}
		}
		_, chain := n.(*ast.BinaryExpr)
		return !chain
	})
	return out
}

// concatenated joins the operands of a + chain, a gap for each that is no
// string literal, and counts the literals.
func concatenated(e ast.Expr, literals *int) string {
	switch e := e.(type) {
	case *ast.ParenExpr:
		return concatenated(e.X, literals)
	case *ast.BinaryExpr:
		if e.Op == token.ADD {
			return concatenated(e.X, literals) + concatenated(e.Y, literals)
		}
	case *ast.BasicLit:
		if s, err := strconv.Unquote(e.Value); e.Kind == token.STRING && err == nil {
			*literals++
			return s
		}
	}
	return " x "
}

// TestOnlyTheStoreTouchesSQL checks that only the sqlite package opens the
// database, and SQL text appears only under `internal/adapter/store`.
func TestOnlyTheStoreTouchesSQL(t *testing.T) {
	t.Parallel()
	used := map[int]bool{}
	forEachFile(t, true, func(fset *token.FileSet, file string, f *ast.File) {
		for _, b := range ruleR13(fset, file, f, sqlAllowlist, used) {
			t.Error(b)
		}
	})
	for _, s := range stale(sqlAllowlist, used) {
		t.Error(s)
	}
}

// ruleStoreWriter flags, in a non-test file outside storecmd.go and the store's
// packages, a call of the store's Open, which makes its caller the writer;
// storecmd.go opens it for the daemon and for the store commands, which hold
// the store's lock. Any other process reads through OpenReadOnly.
func ruleStoreWriter(fset *token.FileSet, file string, f *ast.File) []string {
	if testFile(file) || file == "internal/cli/storecmd.go" || under(path.Dir(file), "internal/adapter/store") {
		return nil
	}
	name := ""
	for _, imp := range f.Imports {
		if p, _ := strconv.Unquote(imp.Path.Value); p == module+"internal/adapter/store/sqlite" {
			name = "sqlite"
			if imp.Name != nil {
				name = imp.Name.Name
			}
		}
	}
	if name == "" {
		return nil
	}
	var out []string
	ast.Inspect(f, func(n ast.Node) bool {
		if sel, ok := n.(*ast.SelectorExpr); ok && sel.Sel.Name == "Open" {
			if x, ok := sel.X.(*ast.Ident); ok && x.Name == name {
				out = append(out, fmt.Sprintf("%s: calls sqlite.Open; only the daemon and the store commands write the store, other processes use OpenReadOnly", fset.Position(sel.Pos())))
			}
		}
		return true
	})
	return out
}

// TestOnlyServeWritesTheStore checks that only `storecmd.go` opens the store as
// its writer, for the daemon and the store commands, and other processes use
// `OpenReadOnly`.
func TestOnlyServeWritesTheStore(t *testing.T) {
	t.Parallel()
	forEachFile(t, false, func(fset *token.FileSet, file string, f *ast.File) {
		for _, b := range ruleStoreWriter(fset, file, f) {
			t.Error(b)
		}
	})
}

// forEachFile parses every non-test Go file of the module, and its test files
// when tests is set, and calls fn with its module-relative path.
func forEachFile(t *testing.T, tests bool, fn func(fset *token.FileSet, file string, f *ast.File)) {
	t.Helper()
	for _, p := range packages(t) {
		self, ok := local(p.ImportPath)
		if !ok {
			continue
		}
		names := p.GoFiles
		if tests {
			names = slices.Concat(names, p.TestGoFiles, p.XTestGoFiles)
		}
		for _, name := range names {
			fset, f := parseFile(t, filepath.Join(p.Dir, name))
			fn(fset, self+"/"+name, f)
		}
	}
}
