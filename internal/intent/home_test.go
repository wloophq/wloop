package intent_test

import (
	"io/fs"
	"os"
	"testing"

	"github.com/wloophq/wloop/internal/domain/decision"
	"github.com/wloophq/wloop/internal/turn"
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

func TestACopiedHomeAndDecisionsKeepTheirTranslation(t *testing.T) {
	files := map[string]string{}
	for _, name := range []string{"home.toml", "decisions.toml"} {
		b, err := fs.ReadFile(turn.BundledFS(), name)
		if err != nil {
			t.Fatal(err)
		}
		files[name] = string(b)
	}
	ui, err := os.ReadFile("../../plugins/github/profile/ui.toml")
	if err != nil {
		t.Fatal(err)
	}
	files["plugins/github/ui.toml"] = string(ui)
	l, problems := loadProfile(t, "pt-BR", files)
	if gh := l.UI["github"]; gh.Decisions["pick-someone"].Clause["other"] != "{counter} esperam um revisor" || gh.Noun["other"] != "pull requests" {
		t.Fatalf("github's copied words in pt-BR: %+v", gh.Decisions)
	}
	if len(problems) > 0 {
		t.Fatalf("problems: %v", problems)
	}
	if l.Home.CaptureHint != "Anote o que está na sua cabeça" || l.Home.Counters[7].Noun["other"] != "tarefas" {
		t.Fatalf("home %+v", l.Home)
	}
	if k := l.Decisions.Kinds[decision.Answer]; k.Queue != "Perguntas do agente" || k.Noun["one"] != "pergunta" {
		t.Fatalf("answer %+v", k)
	}
}

func TestAnUntranslatedUserTextShowsInEnglish(t *testing.T) {
	l, _ := loadProfile(t, "pt-BR", map[string]string{"home.toml": "[capture]\nhint = \"Jot it down\"\n"})
	if l.Home.CaptureHint != "Jot it down" || l.Home.LastJoin != " e " {
		t.Fatalf("home %+v", l.Home)
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
