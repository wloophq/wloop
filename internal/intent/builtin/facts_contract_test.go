package builtin

import (
	dslack "github.com/wloophq/wloop/internal/domain/slack"
	"github.com/wloophq/wloop/internal/domain/unit"
	"slices"
	"testing"
)

func TestIsUntrusted(t *testing.T) {
	for name, want := range map[string]bool{
		"brief.goal":          true,
		"brief.risk":          true,
		"brief.extra.scope":   true,
		"classify.topic":      true,
		"pr.review.action":    true,
		"pr.author":           false,
		"config.cleanup.idle": false,
		"no.such.fact":        false,
	} {
		if got := IsUntrusted(name); got != want {
			t.Errorf("IsUntrusted(%q) = %t, want %t", name, got, want)
		}
	}
}

func TestAnEnumHoldsItsFactsValues(t *testing.T) {
	for _, f := range Facts {
		for p, vs := range f.Enum {
			if len(vs) == 0 {
				t.Errorf("%s: the enum of %q is empty", f.Name, p)
			}
			if len(slices.Compact(slices.Sorted(slices.Values(vs)))) != len(vs) {
				t.Errorf("%s: the enum of %q repeats a value", f.Name, p)
			}
		}
	}
}

func TestEndedFactIsTheSlotsEndedFact(t *testing.T) {
	for p, want := range map[unit.ProviderID]string{unit.GitHub: "pr.ended", dslack.Provider: "slack.ended", unit.Session: "", unit.Task: "", "nowhere": ""} {
		got, ok := EndedFact(p)
		if got != want || ok != (want != "") {
			t.Errorf("EndedFact(%s) = %q, %t; want %q", p, got, ok, want)
		}
	}
}
