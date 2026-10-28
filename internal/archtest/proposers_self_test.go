package archtest

import (
	"go/ast"
	"go/token"
	"go/types"
	"testing"
)

const plantedOrigins = `package draft
type OriginKind string
const (
	OriginAuto OriginKind = "auto"
	OriginUser OriginKind = "user"
)
type Origin struct { Kind OriginKind; Rule string }
type Draft struct { Text string; Origin Origin }
`

func TestDraftTextComesFromTheProfileFlagsPlantedProse(t *testing.T) {
	t.Parallel()
	fset := token.NewFileSet()
	_, _, dp := planted(t, fset, draftPkg, "draft.go", plantedOrigins, nil)
	src := `package x
import (
	"errors"
	"fmt"
	"strings"
	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/pkg/plugin/source"
)
func plugin(n int) source.Draft { return source.Draft{Text: fmt.Sprint("Any news on ", n, "?")} }
func closing(n int) string { return fmt.Sprint("Closing ", n) }
func rendered(s string) (string, error) {
	if s == "" { return "", errors.New("nothing to render") }
	return strings.Join(strings.Fields(s), " "), nil
}
func f(in string) []draft.Draft {
	d := draft.Draft{Text: "Who owns this?"}
	d.Text = fmt.Sprintf("@{%s}, any news?", in)
	t, _ := rendered(in)
	return []draft.Draft{d, {Text: closing(2)}, {Text: in}, {Text: t}}
}
`
	_, _, sp := planted(t, fset, sdkSourcePkg, "source.go", "package source\ntype Draft struct { Text string }\n", nil)
	deps := map[string]*types.Package{draftPkg: dp, sdkSourcePkg: sp}
	for path, body := range map[string]string{
		"fmt":     "func Sprint(a ...any) string { return \"\" }\nfunc Sprintf(f string, a ...any) string { return f }",
		"errors":  "func New(s string) error { return nil }",
		"strings": "func Fields(s string) []string { return nil }\nfunc Join(s []string, sep string) string { return sep }",
	} {
		_, _, deps[path] = planted(t, fset, path, path+".go", "package "+path+"\n"+body, nil)
	}
	f, info, _ := planted(t, fset, module+"internal/x", "x.go", src, deps)
	funcs := map[token.Pos]*ast.FuncDecl{}
	for _, d := range f.Decls {
		if fd, ok := d.(*ast.FuncDecl); ok {
			funcs[fd.Name.Pos()] = fd
		}
	}
	hits := draftTextHits(fset, "internal/x/x.go", info, f, funcs)
	if len(hits) != 4 {
		t.Fatalf("want 4 hits, got %q", hits)
	}
	for want, n := range map[string]int{"a string constant": 1, "a format": 2, "built by closing": 1} {
		flagged(t, want, filter(hits, want), n, want)
	}
}

func TestOnlyTheDraftStepProposesFlagsAPlantedAutoOrigin(t *testing.T) {
	t.Parallel()
	fset := token.NewFileSet()
	_, _, dp := planted(t, fset, draftPkg, "draft.go", plantedOrigins, nil)
	src := `package x
import "github.com/wloophq/wloop/internal/domain/draft"
func f() []draft.Origin {
	return []draft.Origin{{Kind: draft.OriginAuto, Rule: "mine"}, {Kind: draft.OriginUser}, {Rule: "none"}}
}
`
	f, info, _ := planted(t, fset, module+"internal/x", "x.go", src, map[string]*types.Package{draftPkg: dp})
	flagged(t, "auto origin", autoOriginHits(fset, "internal/x/x.go", info, f), 1, "builds an auto draft origin outside draft.propose")
}
