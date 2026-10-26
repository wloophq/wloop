package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/wloophq/wloop/internal/intent/builtin"
	"github.com/wloophq/wloop/internal/intent/expr"
)

const factsPage = "docs/src/content/docs/reference/facts.md"

func TestFactsDocIsCurrent(t *testing.T) {
	doc, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "..", factsPage))
	if err != nil {
		t.Fatal(err)
	}
	want, err := page()
	if err != nil {
		t.Fatal(err)
	}
	if string(doc) != want {
		t.Errorf("%s is not what the fact registry generates; regenerate it with make docs-ref", factsPage)
	}
}

func TestAnIntentFactOutsideEveryGroupFails(t *testing.T) {
	facts := []builtin.Fact{{Name: "pr.mine"}, {Name: "zzz.new"}}
	if _, err := groupedFacts(facts, []group{{"PR", []string{"pr"}}}); err == nil {
		t.Fatal("zzz.new is in no group, and groupedFacts took it")
	}
	if _, err := groupedFacts(facts[:1], []group{{"PR", []string{"pr"}}, {"Gone", []string{"gone"}}}); err == nil {
		t.Fatal("Gone holds no fact, and groupedFacts took it")
	}
}

func TestAWorkflowFactOfAnUntitledSlotFails(t *testing.T) {
	facts := []builtin.Fact{{Name: "zzz.new", Type: expr.String, Slots: []builtin.FactSlot{builtin.WorkflowSlot}, Slot: "zzz"}}
	if _, err := workflowSections(facts, workflowGroups); err == nil {
		t.Fatal("the slot zzz has no title, and workflowSections took it")
	}
}
