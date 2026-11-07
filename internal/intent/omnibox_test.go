package intent_test

import "testing"

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

func TestOmniboxGroupViewMustExist(t *testing.T) {
	l, problems := loadProfile(t, "en", map[string]string{"omnibox.toml": "[[search.groups]]\nid = \"x\"\nlabel = \"X\"\nview = \"waitng\"\n"})
	if _, ok := problemAt(problems, "search.groups[0].view", "waiting"); !ok {
		t.Fatalf("problems %v", problems)
	}
	if len(l.Omnibox.Groups) != 0 {
		t.Fatalf("groups %+v", l.Omnibox.Groups)
	}
}
