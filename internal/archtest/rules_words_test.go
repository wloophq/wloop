package archtest

import (
	"fmt"
	"go/ast"
	"go/token"
	"maps"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// clientTokens are the single words the Go clients quote that no person reads, such as a key's name or an id
// the daemon's API takes: what each is, by literal. Every other single word a client quotes is a catalog key.
var clientTokens = map[string]string{
	"Backspace":     "a key's name as the served bindings spell it",
	"Enter":         "a key's name as the served bindings spell it",
	"Esc":           "a key's name as the served bindings spell it",
	"Tab":           "a key's name as the served bindings spell it",
	"app":           "the name the app subscribes to the daemon's events as",
	"tui":           "the terminal client's name as an events subscriber and a surface",
	"menubar":       "the menu bar's name as a surface",
	"running":       "an app state the status API takes",
	"sweep":         "a job the daemon triggers",
	"true":          "a flag's value an action takes",
	"draft":         "a reply's send mode an action takes",
	"now":           "a reply's send mode an action takes",
	"session":       "the delegate prompt's mode",
	"open":          "an action's id",
	"answer":        "an action's id",
	"detail":        "a command's action id",
	"match":         "a command's action id",
	"review":        "a command's action id and the review's context",
	"capture":       "a command's action id",
	"filter":        "a core action's id",
	"help":          "a core action's id",
	"open-source":   "a core action's id",
	"palette":       "a core action's id",
	"quit":          "a core action's id",
	"edit":          "a draft verb's handler",
	"retry":         "a draft verb's handler",
	"apply":         "a plan action's handler",
	"dismiss":       "a plan action's handler",
	"guide":         "a plan action's handler",
	"reviewed":      "a project verb's handler",
	"sequential":    "a project verb's handler",
	"pending":       "a plan decision the API serves",
	"blocked":       "a plan decision the API serves",
	"plan":          "the plan tab's key",
	"vi":            "the editor run when $VISUAL and $EDITOR are unset",
	"PR":            "the status item's mark before its first count, the same in every language",
	"%.1f GiB":      "a size in a binary unit",
	"%.1f MiB":      "a size in a binary unit",
	"main":          "a native window's internal name, never shown",
	"panel":         "a native window's internal name, never shown",
	"detached ":     "a native window's internal name prefix, never shown",
	"makeKeyWindow": "an AppKit selector",
}

// clientTokenUses is how many literals clientTokens lets pass; a new one raises it on purpose, in review.
const clientTokenUses = 66

var (
	singleWord = regexp.MustCompile(`^[\s(·,;:—–-]*\pL[\pL'’…-]*\pL[\s.,:;!?…)·]*$`)
	wordPrefix = regexp.MustCompile(`^[\pL-]+:$`)
	// formatHole is a format verb or a {name} placeholder, which leaves a single word around it.
	formatHole = regexp.MustCompile(`%[-+# 0-9.*]*[a-zA-Z%]|\{\w+\}`)
)

// wordCalls are the calls whose literal arguments name an Objective-C selector or class, or an environment
// variable.
var wordCalls = map[string]bool{"sel": true, "class": true, "GetClass": true, "RegisterClass": true, "Getenv": true, "LookupEnv": true}

// clientWords are the single-word literals of the Go client file a person may read, and those clientTokens
// lets pass. A literal compared, switched on, used as a key or an index, declared a constant, a catalog key,
// a log's, an Objective-C name, marked proseInternal or a prefix such as "draft:" is none.
func clientWords(fset *token.FileSet, file string, f *ast.File) (flagged, tokens []string) {
	internal := map[int]bool{}
	for _, g := range f.Comments {
		for _, c := range g.List {
			if strings.HasPrefix(c.Text, proseInternal) {
				internal[fset.Position(c.Pos()).Line] = true
			}
		}
	}
	skip := map[*ast.BasicLit]bool{}
	mark := func(n ast.Node) {
		ast.Inspect(n, func(m ast.Node) bool {
			if lit, ok := m.(*ast.BasicLit); ok {
				skip[lit] = true
			}
			return true
		})
	}
	ast.Inspect(f, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.BinaryExpr:
			if x.Op == token.EQL || x.Op == token.NEQ {
				mark(x.X)
				mark(x.Y)
			}
		case *ast.CaseClause:
			for _, e := range x.List {
				mark(e)
			}
		case *ast.IndexExpr:
			mark(x.Index)
		case *ast.KeyValueExpr:
			mark(x.Key)
		case *ast.GenDecl:
			if x.Tok == token.CONST || x.Tok == token.IMPORT {
				mark(x)
			}
		case *ast.Field:
			if x.Tag != nil {
				skip[x.Tag] = true
			}
		case *ast.CallExpr:
			name := ""
			switch fn := x.Fun.(type) {
			case *ast.SelectorExpr:
				name = fn.Sel.Name
			case *ast.Ident:
				name = fn.Name
			}
			if goExempt[name] || wordCalls[name] {
				for _, a := range x.Args {
					mark(a)
				}
			}
		}
		return true
	})
	ast.Inspect(f, func(n ast.Node) bool {
		lit, ok := n.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING || skip[lit] || internal[fset.Position(lit.Pos()).Line] {
			return true
		}
		s, err := strconv.Unquote(lit.Value)
		if err != nil || !singleWord.MatchString(formatHole.ReplaceAllString(s, " ")) || wordPrefix.MatchString(s) || isProse(s) {
			return true
		}
		at := fmt.Sprintf("%s:%d", file, fset.Position(lit.Pos()).Line)
		if clientTokens[s] != "" {
			tokens = append(tokens, s)
			return true
		}
		flagged = append(flagged, fmt.Sprintf("%s quotes %q; a word a person reads goes through texts.Catalog with its key in strings.toml, a token goes in clientTokens", at, s))
		return true
	})
	return flagged, tokens
}

// TestClientsQuoteNoWordOutsideTheCatalog checks that the Go clients quote no single word a person reads
// outside the string catalog (`strings.toml`, through `texts.Catalog`): a single word a client quotes is a
// catalog key, or is compared, switched on, a key, a constant, an Objective-C name, or one of the
// `clientTokens`, which let exactly `clientTokenUses` literals pass.
func TestClientsQuoteNoWordOutsideTheCatalog(t *testing.T) {
	var flagged, tokens []string
	forEachFileOfAnyBuild(t, false, func(fset *token.FileSet, file string, f *ast.File) {
		if strings.HasPrefix(file, "internal/client/") && slices.Contains(proseClients, proseBucket(file)) {
			got, toks := clientWords(fset, file, f)
			flagged, tokens = append(flagged, got...), append(tokens, toks...)
		}
	})
	if len(flagged) > 0 {
		t.Errorf("%d single words outside the catalog:\n%s", len(flagged), strings.Join(flagged, "\n"))
	}
	if len(tokens) != clientTokenUses {
		t.Errorf("clientTokens lets %d literals pass, clientTokenUses is %d", len(tokens), clientTokenUses)
	}
	for _, k := range slices.Sorted(maps.Keys(clientTokens)) {
		if !slices.Contains(tokens, k) {
			t.Errorf("clientTokens names %q, which no client quotes", k)
		}
	}
}
