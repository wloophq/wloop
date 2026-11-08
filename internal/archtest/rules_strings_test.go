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
	"golang.org/x/net/html"
)

// nonProse are literals that read like prose and are not: what each is, by literal.
var nonProse = map[string]string{
	"Escape":                       "KeyboardEvent.key",
	"Enter":                        "KeyboardEvent.key",
	"Backspace":                    "KeyboardEvent.key",
	"Tab":                          "KeyboardEvent.key",
	"Shift":                        "KeyboardEvent.key",
	"Control":                      "KeyboardEvent.key",
	"Meta":                         "KeyboardEvent.key",
	"Alt":                          "KeyboardEvent.key",
	"Delete":                       "KeyboardEvent.key",
	"Home":                         "KeyboardEvent.key",
	"End":                          "KeyboardEvent.key",
	"Accept":                       "an HTTP header name",
	"POST":                         "an HTTP method",
	"GET":                          "an HTTP method",
	"DELETE":                       "an HTTP method",
	"Contents":                     "a macOS bundle's directory",
	"use strict":                   "a script's directive",
	"Esc":                          "the Escape key's name as the served bindings spell it",
	"Plus":                         "the + key's name in aria-keyshortcuts",
	"noopener noreferrer":          "a link's rel tokens",
	"(prefers-color-scheme: dark)": "a CSS media query",
}

// proseClients are the Go clients whose words a person reads; the hook and the CLI's turn client write what
// the command line writes, which stays English.
var proseClients = []string{"menubar", "tui", "window"}

// proseDebts are the prose literals each client still quotes outside the catalog, by script or by client
// package. A count only shrinks: a count above or below the real one fails.
var proseDebts = map[string]int{}

var (
	proseWord   = regexp.MustCompile(`^[("“]?[\pL][\pL'’-]*[.,:;!?…)"”]*$`)
	capitalized = regexp.MustCompile(`^\p{Lu}\p{Ll}{2,}[.,:;!?…]*$`)
	keyValue    = regexp.MustCompile(`^[\w.-]+:\S+$`)
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

// jsExempt are the calls whose first argument is no text a person reads, or is the catalog's key (tKey names a key
// a client words later through t).
var jsExempt = regexp.MustCompile(`(?:\b(?:t|tn|tKey|h|querySelector|querySelectorAll|addEventListener|removeEventListener|getElementById|getJSON|postJSON|sendJSON|fetch|setAttribute|getAttribute|removeAttribute|matches|closest|CustomEvent|createElement)|classList\.(?:add|remove|toggle|contains|replace))\(\s*$|(?:\bclass|className)\s*[:=]\s*$`)

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

// goExempt are the calls whose literal arguments are no text a person reads (logs, panics, the arguments of a
// process), or are the catalog's keys.
var goExempt = map[string]bool{"T": true, "TN": true, "panic": true, "Log": true, "Logf": true, "logf": true,
	"Debug": true, "Info": true, "Warn": true, "Error": true, "Exec": true, "Command": true, "CommandContext": true}

// proseInternal marks a literal no person reads, such as an error that only wraps another, on its line:
// "//prose:internal <why>".
const proseInternal = "//prose:internal "

// ruleGoClientProse flags each prose literal of the Go client file outside the catalog: the catalog's keys,
// logs and literals marked proseInternal are not prose a client shows.
func ruleGoClientProse(fset *token.FileSet, file string, f *ast.File) []string {
	internal := map[int]bool{}
	for _, g := range f.Comments {
		for _, c := range g.List {
			if strings.HasPrefix(c.Text, proseInternal) {
				internal[fset.Position(c.Pos()).Line] = true
			}
		}
	}
	exempt := map[*ast.BasicLit]bool{}
	ast.Inspect(f, func(n ast.Node) bool {
		switch x := n.(type) {
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
		if !ok || lit.Kind != token.STRING || exempt[lit] || internal[fset.Position(lit.Pos()).Line] {
			return true
		}
		s, err := strconv.Unquote(lit.Value)
		if err == nil && isProse(s) {
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

// TestClientsQuoteNoProseOutsideTheCatalog checks that the cockpit's scripts, its page and the Go clients quote no text
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
	page, err := os.ReadFile(filepath.Join("..", "..", webStatic, "index.html"))
	if err != nil {
		t.Fatal(err)
	}
	got["index.html"] = append(got["index.html"], ruleHTMLProse(webStatic+"/index.html", string(page))...)
	forEachFileOfAnyBuild(t, false, func(fset *token.FileSet, file string, f *ast.File) {
		if strings.HasPrefix(file, "internal/client/") && slices.Contains(proseClients, proseBucket(file)) {
			got[proseBucket(file)] = append(got[proseBucket(file)], ruleGoClientProse(fset, file, f)...)
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

var (
	jsKeyCall = regexp.MustCompile(`\b(?:tn?|tKey)\(\s*$`)
	pageKey   = regexp.MustCompile(`data-i18n(?:-[a-z-]+)?="([^"]*)"`)
)

// TestCatalogKeysAreUsedAndKnown checks that every literal key the clients read (`t`, `tn`, `tKey`, `.T`, `.TN`, the
// page's `data-i18n` attributes) is
// a key of `strings.toml`, and every key of it is read by a client or names a text the API or the configuration serves.
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
	page, err := os.ReadFile(filepath.Join("..", "..", webStatic, "index.html"))
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range pageKey.FindAllStringSubmatch(string(page), -1) {
		k := html.UnescapeString(m[1])
		used[k] = true
		if !keys[k] {
			t.Errorf("%s/index.html reads %q, which strings.toml lacks", webStatic, k)
		}
	}
	literals := map[string]bool{}
	forEachFileOfAnyBuild(t, false, func(fset *token.FileSet, file string, f *ast.File) {
		client := strings.HasPrefix(file, "internal/client/")
		if !client && !strings.HasPrefix(file, "internal/api/") && !strings.HasPrefix(file, "internal/config/") {
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

// htmlTextAttrs are the attributes whose value a person reads, each worded by its data-i18n-<attr> key.
var htmlTextAttrs = []string{"title", "placeholder", "aria-label"}

// ruleHTMLProse flags each prose text node or text attribute of the page file whose element carries no
// data-i18n key for it.
func ruleHTMLProse(file, src string) []string {
	var out []string
	z := html.NewTokenizer(strings.NewReader(src))
	var open []html.Token
	line := 1
	for {
		tt := z.Next()
		raw := string(z.Raw())
		switch tt {
		case html.ErrorToken:
			return out
		case html.StartTagToken, html.SelfClosingTagToken:
			tok := z.Token()
			attrs := map[string]string{}
			for _, a := range tok.Attr {
				attrs[a.Key] = a.Val
			}
			for _, name := range htmlTextAttrs {
				if v, ok := attrs[name]; ok && isProse(v) && attrs["data-i18n-"+name] == "" {
					out = append(out, fmt.Sprintf("%s:%d: <%s %s=%q>; a text a person reads carries a data-i18n-%s key in strings.toml", file, line, tok.Data, name, v, name))
				}
			}
			if tt == html.StartTagToken {
				open = append(open, tok)
			}
		case html.EndTagToken:
			if len(open) > 0 {
				open = open[:len(open)-1]
			}
		case html.TextToken:
			text := strings.TrimSpace(html.UnescapeString(raw))
			if len(open) == 0 || !isProse(text) {
				break
			}
			el := open[len(open)-1]
			if el.Data == "script" || el.Data == "style" {
				break
			}
			if !slices.ContainsFunc(el.Attr, func(a html.Attribute) bool { return a.Key == "data-i18n" }) {
				out = append(out, fmt.Sprintf("%s:%d: <%s> says %q; a text a person reads carries a data-i18n key in strings.toml", file, line, el.Data, text))
			}
		}
		line += strings.Count(raw, "\n")
	}
}

// proseInternalMarks is how many literals of the person-facing clients are marked proseInternal; a new mark
// raises it on purpose, in review.
const proseInternalMarks = 12

// TestProseInternalMarksStayFew checks that the clients mark as no text a person reads exactly
// proseInternalMarks literals, so a mark is a reviewed exception.
func TestProseInternalMarksStayFew(t *testing.T) {
	n := 0
	forEachFileOfAnyBuild(t, false, func(_ *token.FileSet, file string, f *ast.File) {
		if !strings.HasPrefix(file, "internal/client/") || !slices.Contains(proseClients, proseBucket(file)) {
			return
		}
		for _, g := range f.Comments {
			for _, c := range g.List {
				if strings.HasPrefix(c.Text, proseInternal) {
					n++
				}
			}
		}
	})
	if n != proseInternalMarks {
		t.Errorf("the clients mark %d literals %q, proseInternalMarks is %d", n, strings.TrimSpace(proseInternal), proseInternalMarks)
	}
}
