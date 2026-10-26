package archtest

import (
	"go/token"
	"go/types"
	"strings"
	"testing"
)

const plantedDraft = `package draft
type Kind string
type Delivery string
const (
	Reply Kind = "reply"
	React Kind = "react"
)
const Reaction Delivery = "reaction"
type Draft struct { Kind Kind; Purpose string; Delivery Delivery; Reaction string }
`

func TestNoDraftKindComparisonsFlagsAPlantedComparison(t *testing.T) {
	t.Parallel()
	fset := token.NewFileSet()
	_, _, dp := planted(t, fset, draftPkg, "draft.go", plantedDraft, nil)
	deps := map[string]*types.Package{draftPkg: dp}
	src := `package x
import "github.com/wloophq/wloop/internal/domain/draft"
func f(d draft.Draft, k draft.Kind) bool {
	_ = draft.Reply
	if d.Kind == "reply" || k != "" { return true }
	switch d.Purpose { case "close": return true }
	if d.Purpose == "" || d.Reaction != "" { return false }
	return d.Delivery == draft.Reaction
}
`
	f, info, _ := planted(t, fset, module+"internal/x", "x.go", src, deps)
	hits := draftKindHits(fset, "internal/x/x.go", info, f)
	if len(hits) != 4 {
		t.Fatalf("want 4 hits, got %q", hits)
	}
	for _, want := range []string{"names the draft kind Reply", "compares a draft's kind", "purpose with a value", "delivery outside"} {
		flagged(t, want, filter(hits, want), 1, want)
	}
	f, info, _ = planted(t, fset, module+"internal/adapter/source/x", "publisher.go", src, deps)
	if got := filter(draftKindHits(fset, "internal/adapter/source/x/publisher.go", info, f), "delivery"); len(got) != 0 {
		t.Fatalf("a publisher compares deliveries: %v", got)
	}
}

func filter(hits []string, want string) []string {
	var out []string
	for _, h := range hits {
		if strings.Contains(h, want) {
			out = append(out, h)
		}
	}
	return out
}
