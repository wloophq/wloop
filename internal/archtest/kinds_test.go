package archtest

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var providerKind = regexp.MustCompile(`^Kind[A-Z]`)

// ruleNoAPIKinds flags a selector api.Kind<Name>: situation kinds belong to
// their provider, never to the API schema.
func ruleNoAPIKinds(rel, src string) []string {
	f, err := parser.ParseFile(token.NewFileSet(), rel, src, parser.SkipObjectResolution)
	if err != nil {
		return []string{fmt.Sprintf("%s: %v", rel, err)}
	}
	var out []string
	ast.Inspect(f, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.SelectorExpr:
			if x, ok := n.X.(*ast.Ident); ok && x.Name == "api" && providerKind.MatchString(n.Sel.Name) {
				out = append(out, fmt.Sprintf("%s: api.%s; a provider's kinds live in its own package", rel, n.Sel.Name))
			}
		case *ast.ValueSpec:
			if strings.HasPrefix(rel, "internal/api/") && !strings.Contains(strings.TrimPrefix(rel, "internal/api/"), "/") {
				for _, name := range n.Names {
					if providerKind.MatchString(name.Name) {
						out = append(out, fmt.Sprintf("%s: api declares %s", rel, name.Name))
					}
				}
			}
		}
		return true
	})
	return out
}

func TestNoProviderKindsInAPI(t *testing.T) {
	root := filepath.Join("..", "..")
	err := filepath.WalkDir(filepath.Join(root, "internal"), func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".go") {
			return err
		}
		raw, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		for _, b := range ruleNoAPIKinds(filepath.ToSlash(rel), string(raw)) {
			t.Error(b)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestNoAPIKindsFlagsAPlantedKind(t *testing.T) {
	use := "package x\nimport \"m/internal/api\"\nvar _ = api.KindDone\nvar _ = api.Kind(\"done\")\n"
	flagged(t, "a use", ruleNoAPIKinds("internal/x/x.go", use), 1, "api.KindDone")
	decl := "package api\nconst KindDone Kind = \"done\"\n"
	flagged(t, "a declaration", ruleNoAPIKinds("internal/api/types.go", decl), 1, "declares KindDone")
}
