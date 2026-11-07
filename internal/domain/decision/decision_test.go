package decision

import (
	"slices"
	"testing"
)

func TestKindsAreTheMasterPlanVocabulary(t *testing.T) {
	want := []Kind{"answer", "approve-plan", "send-draft", "review-step", "last-step", "pick-someone", "act"}
	if !slices.Equal(Kinds, want) {
		t.Fatalf("Kinds = %v, want %v", Kinds, want)
	}
	for _, k := range want {
		if !Valid(k) {
			t.Errorf("Valid(%q) = false", k)
		}
	}
	if Valid("merge") {
		t.Error(`Valid("merge") = true`)
	}
}
