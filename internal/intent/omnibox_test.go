package intent_test

import (
	"maps"
	"slices"
	"testing"

	intentload "github.com/wloophq/wloop/internal/intent"
)

func TestOmniboxLoadsTheBundledGroupsInEachLocale(t *testing.T) {
	en, problems := loadProfile(t, "en", nil)
	if len(problems) > 0 {
		t.Fatal(problems)
	}
	if o := en.Omnibox; o.Limit != 5 || len(o.Groups) != 3 || o.Groups[0].View != "needs-me" || o.Groups[0].Label != "Needs you" || o.Other != "Elsewhere" {
		t.Fatalf("omnibox %+v", o)
	}
	pt, _ := loadProfile(t, "pt-BR", nil)
	if o := pt.Omnibox; o.Groups[1].Label != "Esperando outras pessoas" || o.Other != "Em outros lugares" {
		t.Fatalf("pt-BR omnibox %+v", o)
	}
}

func TestOmniboxBundledVerbs(t *testing.T) {
	l, problems := loadProfile(t, "pt-BR", nil)
	if len(problems) > 0 {
		t.Fatal(problems)
	}
	got := map[string]string{}
	for _, v := range l.Omnibox.Verbs {
		got[v.ID] = v.Action + "|" + v.Verb + "|" + v.Op
	}
	want := map[string]string{"review": "review||", "track": "track||", "done": "|done|", "capture": "||capture", "delegate": "|delegate|start"}
	if !maps.Equal(got, want) {
		t.Fatalf("verbs %v, want %v", got, want)
	}
}

func TestVerbTargetIsExactlyOne(t *testing.T) {
	for body, field := range map[string]string{
		"[verbs.x]\nwords = [\"x\"]\naction = \"review\"\nverb = \"done\"\n": "verbs.x",
		"[verbs.x]\nwords = [\"x\"]\n":                                       "verbs.x",
		"[verbs.x]\nwords = [\"x\"]\naction = \"reveiw\"\n":                  "verbs.x.action",
		"[verbs.x]\nwords = [\"x\"]\nverb = \"dnoe\"\n":                      "verbs.x.verb",
		"[verbs.x]\nwords = [\"two words\"]\nop = \"capture\"\n":             "verbs.x.words",
	} {
		l, problems := loadProfile(t, "en", map[string]string{"omnibox.toml": body})
		if _, ok := problemAt(problems, field, ""); !ok {
			t.Errorf("%q: no problem at %s: %v", body, field, problems)
		}
		if slices.ContainsFunc(l.Omnibox.Verbs, func(v intentload.OmniboxVerbDef) bool { return v.ID == "x" }) {
			t.Errorf("%q: the verb loaded", body)
		}
	}
	_, problems := loadProfile(t, "en", map[string]string{"omnibox.toml": "[verbs.x]\nwords = [\"x\"]\naction = \"reveiw\"\n"})
	if _, ok := problemAt(problems, "verbs.x.action", "review"); !ok {
		t.Errorf("no did-you-mean: %v", problems)
	}
}

func TestVerbRefusesGateResolvingTargets(t *testing.T) {
	l, problems := loadProfile(t, "en", map[string]string{"omnibox.toml": "[verbs.ok]\nwords = [\"ok\"]\nverb = \"approve\"\n"})
	if _, ok := problemAt(problems, "verbs.ok.verb", "resolves a gate"); !ok {
		t.Fatalf("problems %v", problems)
	}
	if slices.ContainsFunc(l.Omnibox.Verbs, func(v intentload.OmniboxVerbDef) bool { return v.ID == "ok" }) {
		t.Fatal("a gate-resolving verb loaded")
	}
}

func TestOmniboxGroupViewMustExist(t *testing.T) {
	l, problems := loadProfile(t, "en", map[string]string{"omnibox.toml": "[[search.groups]]\nid = \"x\"\nlabel = \"X\"\nview = \"waitng\"\n"})
	if _, ok := problemAt(problems, "search.groups[0].view", "waiting"); !ok {
		t.Fatalf("problems %v", problems)
	}
	if len(l.Omnibox.Groups) != 0 {
		t.Fatalf("groups %+v", l.Omnibox.Groups)
	}
}
