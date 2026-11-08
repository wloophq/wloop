package workflow

import (
	"maps"
	"testing"
)

func TestAReasonReadsBackAsItsTemplateAndValues(t *testing.T) {
	for _, c := range []struct {
		key    string
		params map[string]string
	}{
		{ReasonWaits, nil},
		{ReasonFailed, map[string]string{"n": "3", "error": "exit 1: no such file"}},
		{ReasonRetrying, map[string]string{"error": "timeout"}},
		{ReasonHeld, map[string]string{"facts": "pr.ci.failing true, pr.age 3d"}},
		{ReasonHeldRefused, map[string]string{"facts": "pr.ci.failing true", "refusal": "the workspace holds unpushed commits"}},
		{ReasonPerDay, map[string]string{"n": "5"}},
		{ReasonCooling, map[string]string{"duration": "1h0m0s"}},
	} {
		text := Reason(c.key, c.params)
		key, params, ok := ReasonOf(text)
		if !ok || key != c.key || !maps.Equal(params, c.params) && len(params)+len(c.params) > 0 {
			t.Errorf("%q read back as %q %v %v, want %q %v", text, key, params, ok, c.key, c.params)
		}
	}
	if _, _, ok := ReasonOf("the task is not derived from items"); ok {
		t.Error("a refusal no template names read as a reason")
	}
}

func TestEveryReasonIsATemplateItsOwnTextReadsBackAs(t *testing.T) {
	seen := map[string]bool{}
	for _, k := range Reasons {
		if seen[k] {
			t.Errorf("%q twice", k)
		}
		seen[k] = true
		if got, _, ok := ReasonOf(k); !ok || got != k {
			t.Errorf("the template %q reads back as %q", k, got)
		}
	}
}
