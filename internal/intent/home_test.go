package intent_test

import (
	"testing"

	"github.com/wloophq/wloop/internal/domain/decision"
)

func TestHomeLoadsTheBundledBriefingInEachLocale(t *testing.T) {
	en, problems := loadProfile(t, "en", nil)
	if len(problems) > 0 {
		t.Fatalf("problems: %v", problems)
	}
	h := en.Home
	if len(h.Counters) != 9 || h.Counters[0].Kind != decision.Answer || h.Counters[0].ID != "answer" || h.Counters[7].View != "in-loop" ||
		len(h.Sections) != 2 || h.Sections[0].View != "home-blocked" || h.Sections[0].Time != "owed" || h.LastJoin != " and " {
		t.Fatalf("home = %+v", h)
	}
	pt, problems := loadProfile(t, "pt-BR", nil)
	if len(problems) > 0 {
		t.Fatalf("problems: %v", problems)
	}
	if h := pt.Home; h.LastJoin != " e " || h.CaptureHint != "Anote o que está na sua cabeça" || h.Counters[8].Clause["other"] != "{counter} estão com outras pessoas" {
		t.Fatalf("pt-BR home = %+v", h)
	}
}

func TestHomeCounterSetsKindOrView(t *testing.T) {
	l, problems := loadProfile(t, "en", map[string]string{"home.toml": "[[counters]]\nkind = \"answer\"\nview = \"waiting\"\n\n[[counters]]\nkind = \"anwser\"\n\n[[counters]]\nkind = \"act\"\n"})
	if _, ok := problemAt(problems, "counters[0]", "kind or view"); !ok {
		t.Errorf("both set: %v", problems)
	}
	if _, ok := problemAt(problems, "counters[1].kind", "answer"); !ok {
		t.Errorf("no did-you-mean: %v", problems)
	}
	if len(l.Home.Counters) != 1 || l.Home.Counters[0].Kind != decision.Act {
		t.Fatalf("counters = %+v, want the user's list, its good counter alone", l.Home.Counters)
	}
}

func TestHomeViewsMustExist(t *testing.T) {
	l, problems := loadProfile(t, "en", map[string]string{"home.toml": "[[sections]]\nid = \"x\"\nview = \"nope\"\n\n[[counters]]\nid = \"y\"\nview = \"waitng\"\nnoun = \"task\"\nclause = \"{counter} wait\"\n"})
	if _, ok := problemAt(problems, "sections[0].view", "names no view"); !ok {
		t.Errorf("section: %v", problems)
	}
	if _, ok := problemAt(problems, "counters[0].view", "waiting"); !ok {
		t.Errorf("counter: %v", problems)
	}
	if len(l.Home.Sections) != 0 || len(l.Home.Counters) != 0 {
		t.Fatalf("home = %+v", l.Home)
	}
}
