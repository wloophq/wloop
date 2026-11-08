package omnibox_test

import (
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/core/omnibox"
	"github.com/wloophq/wloop/internal/domain/unit"
)

func vocabulary() omnibox.Vocabulary {
	return omnibox.Vocabulary{
		Verbs: []omnibox.Verb{
			{ID: "review", Words: []string{"review", "revise", "revisar"}, Action: "review"},
			{ID: "done", Words: []string{"done", "close", "feche"}, TaskVerb: "done"},
			{ID: "capture", Words: []string{"capture", "anote"}, Op: omnibox.OpCapture},
			{ID: "delegate", Words: []string{"delegate", "delegue"}, TaskVerb: "delegate", Op: omnibox.OpStart},
		},
		Fold: func(s string) string { return strings.ToLower(strings.ReplaceAll(s, "é", "e")) },
	}
}

func TestVocabularyMatchesAnyLanguage(t *testing.T) {
	v := vocabulary()
	for _, w := range []string{"revise", "Review", "REVISAR"} {
		if got, ok := v.Match(w); !ok || got.ID != "review" {
			t.Errorf("Match(%q) = %+v %v", w, got, ok)
		}
	}
	if got, ok := v.Match("Fêche"); ok {
		t.Errorf("an unknown word matched %+v", got)
	}
	if _, ok := v.Match("merge"); ok {
		t.Error("merge matched")
	}
}

func TestVocabularyAllowsOnlyItsTargets(t *testing.T) {
	v := vocabulary()
	u := unit.Unit{Provider: "alpha", Key: "acme/api#8"}
	for _, c := range []struct {
		op   omnibox.AskOp
		want string
	}{
		{omnibox.AskOp{Op: "action", Unit: &u, Action: "review"}, ""},
		{omnibox.AskOp{Op: "action", Unit: &u, Action: "merge"}, omnibox.DropOutsideVocabulary},
		{omnibox.AskOp{Op: "verb", Task: "t-1", Verb: "done"}, ""},
		{omnibox.AskOp{Op: "verb", Task: "t-1", Verb: "delegate"}, ""},
		{omnibox.AskOp{Op: "verb", Task: "t-1", Verb: "approve"}, omnibox.DropOutsideVocabulary},
		{omnibox.AskOp{Op: "capture", Title: "call Ada"}, ""},
		{omnibox.AskOp{Op: "start", Brief: "add retries"}, ""},
		{omnibox.AskOp{Op: "verb", Task: "t-1", Verb: "review"}, omnibox.DropOutsideVocabulary},
	} {
		why, params := v.Allowed(c.op)
		if why != c.want {
			t.Errorf("%+v: %q %v, want %q", c.op, why, params, c.want)
		}
		if why != "" && params["name"] == "" {
			t.Errorf("%+v: a refusal names nothing", c.op)
		}
	}
}
