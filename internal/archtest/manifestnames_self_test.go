package archtest

import (
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"testing"
)

// plantedPkg is src type-checked as the package at path, which imports nothing.
func plantedPkg(t *testing.T, path, src string) (*token.FileSet, *ast.File, *types.Info) {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "planted.go", src, 0)
	if err != nil {
		t.Fatal(err)
	}
	info := &types.Info{Types: map[ast.Expr]types.TypeAndValue{}, Uses: map[*ast.Ident]types.Object{}, Defs: map[*ast.Ident]types.Object{}}
	if _, err := (&types.Config{}).Check(path, fset, []*ast.File{f}, info); err != nil {
		t.Fatal(err)
	}
	return fset, f, info
}

var plantedNames = manifestNames{literal: []string{"slack_draft", "gh pr review"}, factPrefixes: []string{"draft.github."}, typed: []string{"review", "message"}}

func TestArchNoManifestNamesInCoreFlagsAPlantedSlackDraft(t *testing.T) {
	t.Parallel()
	fset, f, info := plantedPkg(t, "m/internal/core/x", "package x\nvar tools = []string{\"slack_draft\"}\n")
	flagged(t, "a planted tool name", manifestNameHits(fset, "internal/core/x/x.go", info, f, plantedNames), 1, `"slack_draft"`)

	fset, f, info = plantedPkg(t, "m/internal/core/x", "package x\ntype T struct{ A string `slack_draft` }\nvar ok = []string{\"review\", \"message\", \"draft.slack.x\"}\n")
	flagged(t, "a tag and core words", manifestNameHits(fset, "internal/core/x/x.go", info, f, plantedNames), 0, "")

	fset, f, info = plantedPkg(t, "m/internal/core/x", "package x\nvar a, b = \"gh pr review\", \"draft.github.review.event\"\n")
	flagged(t, "a write command and a draft fact", manifestNameHits(fset, "internal/core/x/x.go", info, f, plantedNames), 2, "its plugin manifest declares")

	src := "package draft\ntype Kind string\ntype Delivery string\nconst Review Kind = \"review\"\nvar d = Delivery(\"message\")\nvar k Kind = \"nudge\"\nvar s = string(Review)\n"
	fset, f, info = plantedPkg(t, draftPkg, src)
	flagged(t, "typed kinds and deliveries", manifestNameHits(fset, "internal/domain/draft/x.go", info, f, plantedNames), 3, "draft kind or delivery")
}

func TestArchNoDeliveryListFlagsAPlantedList(t *testing.T) {
	t.Parallel()
	src := "package draft\ntype Kind string\ntype Delivery string\nconst Message Delivery = \"message\"\nvar All = []Delivery{Message, \"reaction\"}\nvar Ks = []Kind{\"reply\"}\nvar none []Kind\nvar m = Message\n"
	fset, f, info := plantedPkg(t, draftPkg, src)
	flagged(t, "lists in the draft package", draftListHits(fset, "internal/domain/draft/x.go", info, f), 2, "lists draft")
	flagged(t, "lists and a constant elsewhere", draftListHits(fset, "internal/core/x/x.go", info, f), 4, "")
}
