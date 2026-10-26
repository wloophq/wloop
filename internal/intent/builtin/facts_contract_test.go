package builtin

import (
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
