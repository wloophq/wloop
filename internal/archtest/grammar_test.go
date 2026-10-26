package archtest

import (
	"fmt"
	"go/ast"
	"go/token"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/intent/builtin"
)

// profileDecoders are the packages that read profile files: the loaders,
// which check each file against the grammar, and the front matter reader
// they share.
var profileDecoders = []string{"internal/intent", "internal/profile", "internal/frontmatter"}

// decodeCalls are the calls that decode a profile file's TOML.
var decodeCalls = map[string][]string{"toml": {"Unmarshal", "NewDecoder"}, "frontmatter": {"Decode"}}

// grammarWords are the key segments of every row of builtin.Grammar.
func grammarWords() map[string]bool {
	out := map[string]bool{}
	for _, k := range builtin.Grammar {
		for _, seg := range strings.Split(strings.ReplaceAll(k.Path, "[]", ""), ".") {
			out[seg] = true
		}
	}
	return out
}

// ruleGrammarKeys flags, in the file rel, a toml key no grammar row names,
// and a toml key or a decode of a profile file outside the profile's loaders.
func ruleGrammarKeys(fset *token.FileSet, rel string, f *ast.File, words map[string]bool) []string {
	loader := under(filepath.ToSlash(filepath.Dir(rel)), profileDecoders...)
	var out []string
	ast.Inspect(f, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.Field:
			if n.Tag == nil {
				return true
			}
			tag, err := strconv.Unquote(n.Tag.Value)
			if err != nil {
				return true
			}
			key, _, _ := strings.Cut(reflect.StructTag(tag).Get("toml"), ",")
			switch {
			case key == "" || key == "-":
			case !loader:
				out = append(out, fmt.Sprintf("%s: toml key %s outside the profile's loaders; a profile file is read by internal/intent or internal/profile, against the grammar (R-grammar)", fset.Position(n.Pos()), key))
			case !words[key]:
				out = append(out, fmt.Sprintf("%s: toml key %s has no row in builtin.Grammar (R-grammar)", fset.Position(n.Pos()), key))
			}
		case *ast.CallExpr:
			sel, ok := n.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			x, ok := sel.X.(*ast.Ident)
			if !ok || loader {
				return true
			}
			for _, name := range decodeCalls[x.Name] {
				if sel.Sel.Name == name {
					out = append(out, fmt.Sprintf("%s: %s.%s decodes a profile file outside the profile's loaders (R-grammar)", fset.Position(n.Pos()), x.Name, name))
				}
			}
		}
		return true
	})
	return out
}

// TestManifestKeysAreInTheGrammar checks that every toml key the module
// decodes is a word of builtin.Grammar and that only internal/intent,
// internal/profile and internal/frontmatter decode a profile file's TOML,
// so no manifest key is read outside the table the loaders check, the
// schemas and the reference pages derive from. The configuration's keys
// are checked against the table by internal/config's
// TestEveryConfigKeyIsInTheGrammar.
func TestManifestKeysAreInTheGrammar(t *testing.T) {
	t.Parallel()
	words := grammarWords()
	for _, p := range packages(t) {
		dir, ok := local(p.ImportPath)
		if !ok {
			continue
		}
		for _, name := range p.GoFiles {
			path := filepath.Join(p.Dir, name)
			fset, f := parseFile(t, path)
			for _, b := range ruleGrammarKeys(fset, filepath.Join(dir, name), f, words) {
				t.Error(b)
			}
		}
	}
}
