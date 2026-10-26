package api

import "testing"

func TestARefuseCaseCodeReadsBackItsIndex(t *testing.T) {
	for code, want := range map[string]int{RefuseCase(0): 0, RefuseCase(12): 12} {
		if i, ok := RefuseCaseIndex(code); !ok || i != want {
			t.Errorf("%s: %d %v", code, i, ok)
		}
	}
	for _, code := range []string{"", RefusalNotOffered, BuiltinWhy("take_over"), "action.refuse[x]", "action.refuse[-1]"} {
		if _, ok := RefuseCaseIndex(code); ok {
			t.Errorf("%q reads as a refuse case", code)
		}
	}
}
