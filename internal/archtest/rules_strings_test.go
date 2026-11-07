package archtest

import (
	"fmt"
	"go/ast"
	"go/token"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/pelletier/go-toml/v2"
)

// nonProse are literals that read like prose and are not: what each is, by literal.
var nonProse = map[string]string{
	"Escape":    "KeyboardEvent.key",
	"Enter":     "KeyboardEvent.key",
	"Backspace": "KeyboardEvent.key",
	"Tab":       "KeyboardEvent.key",
	"Shift":     "KeyboardEvent.key",
	"Control":   "KeyboardEvent.key",
	"Meta":      "KeyboardEvent.key",
	"Alt":       "KeyboardEvent.key",
	"Delete":    "KeyboardEvent.key",
	"Home":      "KeyboardEvent.key",
	"End":       "KeyboardEvent.key",
	"Accept":    "an HTTP header name",
	"POST":      "an HTTP method",
	"GET":       "an HTTP method",
	"DELETE":    "an HTTP method",
	"Contents":  "a macOS bundle's directory",
}

// proseClients are the Go clients whose words a person reads; the hook and the CLI's turn client write what
// the command line writes, which stays English.
var proseClients = []string{"menubar", "tui", "window"}

// proseDebts are the prose literals each client still quotes outside the catalog, by script or by client
// package. A count only shrinks: a count above or below the real one fails.
var proseDebts = map[string]int{
	"app.js": 196, "dash.js": 60, "detail.js": 33, "keys.js": 20, "palette.js": 6, "panel.js": 1, "plugins.js": 6, "review.js": 11,
	"routes.js": 1, "task.js": 38, "update.js": 1, "views.js": 18, "tui": 87,
}

var (
	proseWord   = regexp.MustCompile(`^[\pL][\pL'’-]*[.,:;!?…)]*$`)
	capitalized = regexp.MustCompile(`^\p{Lu}\p{Ll}{2,}[.,:;!?…]*$`)
	keyValue    = regexp.MustCompile(`^[\w.-]+:[^\s]*$`)
)

// isProse says whether literal s is a text a person reads: two or more words of letters, or one capitalized
// word of three letters or more; never a URL, a path, a selector, a query term or a nonProse entry.
func isProse(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" || nonProse[s] != "" || strings.Contains(s, "://") || strings.HasPrefix(s, "/") || strings.ContainsAny(s[:1], ".#[") {
		return false
	}
	fields := strings.Fields(s)
	if slices.ContainsFunc(fields, func(f string) bool { return keyValue.MatchString(f) }) && len(fields) <= 3 {
		return false
	}
	words := 0
	for _, f := range fields {
		if proseWord.MatchString(f) {
			words++
		}
	}
	if words >= 2 {
		return true
	}
	return len(fields) == 1 && capitalized.MatchString(fields[0])
}

// jsLiteral is a string literal of a script, the text of a template literal's parts, and the code before it.
type jsLiteral struct {
	text   string
	line   int
	before string
}

// jsLiterals are the string and template literals of src, comments and regular expressions skipped.
func jsLiterals(src string) []jsLiteral {
	var out []jsLiteral
	line := 1
	last := 0 // start of the code since the previous literal
	prevSignificant := byte(0)
	for i := 0; i < len(src); i++ {
		c := src[i]
		switch {
		case c == '\n':
			line++
		case c == '/' && i+1 < len(src) && src[i+1] == '/':
			for i < len(src) && src[i] != '\n' {
				i++
			}
			i--
			continue
		case c == '/' && i+1 < len(src) && src[i+1] == '*':
			end := strings.Index(src[i+2:], "*/")
			if end < 0 {
				return out
			}
			line += strings.Count(src[i:i+2+end+2], "\n")
			i += 2 + end + 1
			continue
		case c == '/' && strings.IndexByte("(,=:[!&|?{};+-*%<>~^", prevSignificant) >= 0:
			inClass := false
			for i++; i < len(src) && src[i] != '\n'; i++ {
				if src[i] == '\\' {
					i++
				} else if src[i] == '[' {
					inClass = true
				} else if src[i] == ']' {
					inClass = false
				} else if src[i] == '/' && !inClass {
					break
				}
			}
			prevSignificant = ')'
			continue
		case c == '"' || c == '\'' || c == '`':
			start, startLine := i, line
			var b strings.Builder
			for i++; i < len(src) && src[i] != c; i++ {
				switch {
				case src[i] == '\\' && i+1 < len(src):
					i++
					b.WriteByte(src[i])
				case c == '`' && src[i] == '$' && i+1 < len(src) && src[i+1] == '{':
					depth := 0
					for ; i < len(src); i++ {
						if src[i] == '{' {
							depth++
						} else if src[i] == '}' {
							if depth--; depth == 0 {
								break
							}
						} else if src[i] == '\n' {
							line++
						}
					}
					b.WriteString(" ")
				default:
					if src[i] == '\n' {
						line++
					}
					b.WriteByte(src[i])
				}
			}
			out = append(out, jsLiteral{text: b.String(), line: startLine, before: src[last:start]})
			last = i + 1
			prevSignificant = ')'
			continue
		}
		if c != ' ' && c != '\t' && c != '\n' && c != '\r' {
			prevSignificant = c
		}
	}
	return out
}

// jsExempt are the calls whose first argument is no text a person reads, or is the catalog's key.
var jsExempt = regexp.MustCompile(`(?:\b(?:t|tn|h|querySelector|querySelectorAll|addEventListener|removeEventListener|getElementById|getJSON|postJSON|sendJSON|fetch|setAttribute|getAttribute|removeAttribute|matches|closest|CustomEvent|createElement)|classList\.(?:add|remove|toggle|contains|replace))\(\s*$|(?:\bclass|className)\s*[:=]\s*$`)

// ruleClientProse flags each prose literal of the script file outside the catalog.
func ruleClientProse(file string, src string) []string {
	var out []string
	for _, l := range jsLiterals(src) {
		if jsExempt.MatchString(l.before) || !isProse(l.text) {
			continue
		}
		out = append(out, fmt.Sprintf("%s:%d quotes %q; a text a person reads goes through t or tn with its key in strings.toml", file, l.line, l.text))
	}
	return out
}

// goExempt are the calls whose literal arguments are no text a person reads, or are the catalog's keys.
var goExempt = map[string]bool{"T": true, "TN": true, "New": true, "panic": true, "Errorf": true, "Log": true, "Logf": true, "logf": true, "Printf": true, "Println": true,
	"Debug": true, "Info": true, "Warn": true, "Error": true, "Sprintf": true, "Fprintf": true, "Exec": true, "Command": true, "CommandContext": true}

// ruleGoClientProse flags each prose literal of the Go client file outside the catalog: the catalog's keys
// (keys), errors, logs and formats are not prose a client shows itself.
func ruleGoClientProse(fset *token.FileSet, file string, f *ast.File, keys map[string]bool) []string {
	exempt := map[*ast.BasicLit]bool{}
	exemptAll := func(n ast.Node) {
		ast.Inspect(n, func(m ast.Node) bool {
			if lit, ok := m.(*ast.BasicLit); ok {
				exempt[lit] = true
			}
			return true
		})
	}
	ast.Inspect(f, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.FuncDecl:
			if x.Name.Name == "Error" && x.Recv != nil {
				exemptAll(x.Body)
			}
		case *ast.CallExpr:
			name := ""
			switch fn := x.Fun.(type) {
			case *ast.SelectorExpr:
				name = fn.Sel.Name
			case *ast.Ident:
				name = fn.Name
			}
			if goExempt[name] {
				for _, a := range x.Args {
					ast.Inspect(a, func(m ast.Node) bool {
						if lit, ok := m.(*ast.BasicLit); ok {
							exempt[lit] = true
						}
						return true
					})
				}
			}
		case *ast.ImportSpec:
			exempt[x.Path] = true
		case *ast.Field:
			if x.Tag != nil {
				exempt[x.Tag] = true
			}
		}
		return true
	})
	var out []string
	ast.Inspect(f, func(n ast.Node) bool {
		lit, ok := n.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING || exempt[lit] {
			return true
		}
		s, err := strconv.Unquote(lit.Value)
		if err == nil && !keys[s] && isProse(s) {
			out = append(out, fmt.Sprintf("%s:%d quotes %q; a text a person reads goes through texts.Catalog with its key in strings.toml", file, fset.Position(lit.Pos()).Line, s))
		}
		return true
	})
	return out
}

// proseBucket is the debt bucket of a client file: a script's name, or the client package under
// internal/client.
func proseBucket(file string) string {
	if strings.HasPrefix(file, webStatic+"/") {
		return strings.TrimPrefix(file, webStatic+"/")
	}
	rest := strings.TrimPrefix(file, "internal/client/")
	return strings.SplitN(rest, "/", 2)[0]
}

func cockpitScripts(t *testing.T) map[string]string {
	t.Helper()
	files, err := filepath.Glob(filepath.Join("..", "..", webStatic, "*.js"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no scripts under %s: %v", webStatic, err)
	}
	out := map[string]string{}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		out[webStatic+"/"+filepath.Base(f)] = string(b)
	}
	return out
}

// TestClientsQuoteNoProseOutsideTheCatalog checks that the cockpit's scripts and the Go clients quote no text
// a person reads outside the string catalog (`strings.toml`, through `t`/`tn` and `texts.Catalog`), but for
// the debts `proseDebts` counts, which only shrink.
func TestClientsQuoteNoProseOutsideTheCatalog(t *testing.T) {
	got := map[string][]string{}
	for file, src := range cockpitScripts(t) {
		if strings.HasSuffix(file, "/i18n.js") || strings.Contains(file, "/vendor/") {
			continue
		}
		got[proseBucket(file)] = append(got[proseBucket(file)], ruleClientProse(file, src)...)
	}
	keys := catalogKeys(t)
	forEachFile(t, false, func(fset *token.FileSet, file string, f *ast.File) {
		if strings.HasPrefix(file, "internal/client/") && slices.Contains(proseClients, proseBucket(file)) {
			got[proseBucket(file)] = append(got[proseBucket(file)], ruleGoClientProse(fset, file, f, keys)...)
		}
	})
	for _, b := range slices.Sorted(maps.Keys(got)) {
		if n := len(got[b]); n != proseDebts[b] {
			t.Errorf("%s quotes %d texts outside the catalog, its debt is %d:\n%s", b, n, proseDebts[b], strings.Join(got[b], "\n"))
		}
	}
	for b := range proseDebts {
		if _, ok := got[b]; !ok {
			t.Errorf("proseDebts names %s, which no client file is", b)
		}
	}
}

// catalogKeys are the keys of the bundled strings.toml.
func catalogKeys(t *testing.T) map[string]bool {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "profiles", "default", "strings.toml"))
	if err != nil {
		t.Fatal(err)
	}
	var doc struct{ Strings map[string]any }
	if err := toml.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	out := map[string]bool{}
	for k := range doc.Strings {
		out[k] = true
	}
	return out
}

var jsKeyCall = regexp.MustCompile(`\bt(?:n)?\(\s*$`)

// TestCatalogKeysAreUsedAndKnown checks that every literal key the clients read (`t`, `tn`, `.T`, `.TN`) is
// a key of `strings.toml`, and every key of it is read by a client or names a text the API serves.
func TestCatalogKeysAreUsedAndKnown(t *testing.T) {
	keys := catalogKeys(t)
	used := map[string]bool{}
	for file, src := range cockpitScripts(t) {
		for _, l := range jsLiterals(src) {
			if jsKeyCall.MatchString(l.before) {
				used[l.text] = true
				if !keys[l.text] {
					t.Errorf("%s:%d reads %q, which strings.toml lacks", file, l.line, l.text)
				}
			}
		}
	}
	literals := map[string]bool{}
	forEachFile(t, false, func(fset *token.FileSet, file string, f *ast.File) {
		client := strings.HasPrefix(file, "internal/client/")
		if !client && !strings.HasPrefix(file, "internal/api/") {
			return
		}
		ast.Inspect(f, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.BasicLit:
				if s, err := strconv.Unquote(x.Value); err == nil && x.Kind == token.STRING {
					literals[s] = true
				}
			case *ast.CallExpr:
				sel, ok := x.Fun.(*ast.SelectorExpr)
				if !client || !ok || (sel.Sel.Name != "T" && sel.Sel.Name != "TN") || len(x.Args) == 0 {
					return true
				}
				if lit, ok := x.Args[0].(*ast.BasicLit); ok {
					s, _ := strconv.Unquote(lit.Value)
					used[s] = true
					if !keys[s] {
						t.Errorf("%s:%d reads %q, which strings.toml lacks", file, fset.Position(lit.Pos()).Line, s)
					}
				}
			}
			return true
		})
	})
	for _, k := range slices.Sorted(maps.Keys(keys)) {
		if !used[k] && !literals[k] {
			t.Errorf("strings.toml's %q is read by no client", k)
		}
	}
}
