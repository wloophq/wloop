package archtest

import (
	"go/parser"
	"go/token"
	"testing"
)

func TestRGrammarFlagsAKeyOutsideTheTableAndAForeignDecoder(t *testing.T) {
	t.Parallel()
	src := "package x\n" +
		"type m struct {\n\tLabel string `toml:\"label\"`\n\tColour string `toml:\"colour\"`\n\tSkip string `toml:\"-\"`\n}\n" +
		"func f(b []byte, v any) { _ = toml.Unmarshal(b, v); _ = frontmatter.Decode(\"f\", b, 1, v); _ = json.Unmarshal(b, v) }\n"
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "x.go", src, 0)
	if err != nil {
		t.Fatal(err)
	}
	words := map[string]bool{"label": true}
	flagged(t, "a loader", ruleGrammarKeys(fset, "internal/intent/x.go", f, words), 1, "toml key colour has no row")
	flagged(t, "a foreign package", ruleGrammarKeys(fset, "internal/cli/x.go", f, words), 4, "(R-grammar)")
	flagged(t, "the plugin SDK", ruleGrammarKeys(fset, "pkg/plugin/manifest/x.go", f, words), 0, "")
}
