package intent_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/domain/decision"
	"github.com/wloophq/wloop/internal/domain/gate"
	intentload "github.com/wloophq/wloop/internal/intent"
	"github.com/wloophq/wloop/internal/profile"
	"github.com/wloophq/wloop/internal/turn"
)

// loadProfile loads the bundled chain in locale under a user profile holding files, its plugins/<id>/ files
// read as the user's layer of that plugin.
func loadProfile(t *testing.T, locale string, files map[string]string) (*intentload.Loaded, intentload.Problems) {
	t.Helper()
	d := t.TempDir()
	plugins := map[string]bool{}
	for name, body := range files {
		f := filepath.Join(d, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(f), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(f, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		if rest, ok := strings.CutPrefix(name, "plugins/"); ok {
			plugins[strings.SplitN(rest, "/", 2)[0]] = true
		}
	}
	layers := append(profile.BundledChain(), profile.Layer{Name: "user", Source: d, Dir: d})
	for id := range plugins {
		p := filepath.Join(d, "plugins", id)
		layers = append(layers, profile.Layer{Name: "user:" + id, Source: p, Dir: p})
	}
	base, err := turn.Base()
	if err != nil {
		t.Fatal(err)
	}
	return intentload.LoadIn(locale, layers, turn.BundledFS(), base, turn.CheckTemplate)
}

// problemAt is the first problem whose field is field and whose message holds want.
func problemAt(ps intentload.Problems, field, want string) (intentload.Problem, bool) {
	for _, p := range ps {
		if p.Field == field && strings.Contains(p.Msg, want) {
			return p, true
		}
	}
	return intentload.Problem{}, false
}

func TestDecisionsLoadsDefaultAndPluginLayers(t *testing.T) {
	l, problems := loadProfile(t, "en", nil)
	if len(problems) > 0 {
		t.Fatalf("the bundled profile has problems: %v", problems)
	}
	d := l.Decisions
	if d.Map.Gates[gate.KindQuestion] != decision.Answer || d.Map.Situations["github"]["merge"] != decision.LastStep ||
		d.Map.Signals["github"]["ready"] != decision.LastStep || d.Map.Situations["slack"]["waits_on_viewer"] != decision.Answer {
		t.Fatalf("map = %+v", d.Map)
	}
	if d.Primary["github"][decision.LastStep] != "open" {
		t.Fatalf("primary = %v", d.Primary)
	}
	if k := d.Kinds[decision.Answer]; k.View != "decide-answer" || k.Queue != "Agent questions" || k.Noun["one"] != "question" {
		t.Fatalf("answer = %+v", k)
	}
}

func TestDecisionsReadInPtBR(t *testing.T) {
	l, problems := loadProfile(t, "pt-BR", nil)
	if len(problems) > 0 {
		t.Fatalf("problems: %v", problems)
	}
	k := l.Decisions.Kinds[decision.ApprovePlan]
	if k.Queue != "Planos para aprovar" || k.Noun["other"] != "planos" || k.Clause["other"] != "{counter} esperam sua aprovação" {
		t.Fatalf("approve-plan in pt-BR = %+v", k)
	}
	if gh := l.UI["github"]; gh.Noun["other"] != "pull requests" || gh.Decisions["last-step"].Queue != "Prontos para merge" {
		t.Fatalf("github words in pt-BR = %+v %+v", gh.Noun, gh.Decisions)
	}
}

func TestDecisionsUserLayerOverridesPluginMapping(t *testing.T) {
	l, problems := loadProfile(t, "en", map[string]string{"plugins/github/decisions.toml": "[situations]\nmerge = \"act\"\n"})
	if len(problems) > 0 {
		t.Fatalf("problems: %v", problems)
	}
	if got := l.Decisions.Map.Situations["github"]; got["merge"] != decision.Act || got["no_reviewer"] != decision.PickSomeone {
		t.Fatalf("situations = %v", got)
	}
}

func TestDecisionsRefusesUnknownKind(t *testing.T) {
	l, problems := loadProfile(t, "en", map[string]string{"plugins/github/decisions.toml": "[situations]\nmerge = \"last-stpe\"\n"})
	if _, ok := problemAt(problems, "situations.merge", "last-step"); !ok {
		t.Fatalf("no did-you-mean on the kind: %v", problems)
	}
	if got := l.Decisions.Map.Situations["github"]["merge"]; got != decision.LastStep {
		t.Fatalf("a refused mapping replaced the lower one: %q", got)
	}
}

func TestDecisionsRefusesUndeclaredSituation(t *testing.T) {
	_, problems := loadProfile(t, "en", map[string]string{"plugins/github/decisions.toml": "[situations]\nmergd = \"last-step\"\n"})
	p, ok := problemAt(problems, "situations.mergd", "merge")
	if !ok || !strings.Contains(p.Msg, "no_reviewer") {
		t.Fatalf("no problem naming the manifest's situations: %v", problems)
	}
}

func TestDecisionsRefusesSituationsInDefaultLayer(t *testing.T) {
	_, problems := loadProfile(t, "en", map[string]string{"decisions.toml": "[situations]\nmerge = \"last-step\"\n"})
	if _, ok := problemAt(problems, "situations", "belong to a plugin's layer"); !ok {
		t.Fatalf("problems: %v", problems)
	}
}

func TestDecisionsKindViewMustExist(t *testing.T) {
	_, problems := loadProfile(t, "en", map[string]string{"decisions.toml": "[kinds.answer]\nview = \"nope\"\n"})
	if _, ok := problemAt(problems, "kinds.answer.view", "names no view"); !ok {
		t.Fatalf("problems: %v", problems)
	}
}

func TestDecisionsPrimaryIsActionOrOpen(t *testing.T) {
	l, problems := loadProfile(t, "en", map[string]string{"plugins/github/decisions.toml": "[primary]\nlast-step = \"merge\"\npick-someone = \"review\"\n"})
	if _, ok := problemAt(problems, "primary.last-step", "no catalog action"); !ok {
		t.Fatalf("merge accepted: %v", problems)
	}
	if _, ok := problemAt(problems, "primary.pick-someone", ""); ok {
		t.Fatalf("review, a catalog action, refused: %v", problems)
	}
	if got := l.Decisions.Primary["github"]; got[decision.LastStep] != "" || got[decision.PickSomeone] != "review" {
		t.Fatalf("primary = %v", got)
	}
}

func TestUITextsReadNounAndDecisionCopy(t *testing.T) {
	l, _ := loadProfile(t, "en", nil)
	gh := l.UI["github"]
	if gh.Noun["other"] != "pull requests" || gh.Decisions["last-step"].Queue != "Ready to merge" ||
		gh.Decisions["last-step"].Clause["one"] != "{counter} only waits for the merge" {
		t.Fatalf("github = %+v %+v", gh.Noun, gh.Decisions)
	}
}

func TestPluralNeedsOther(t *testing.T) {
	_, problems := loadProfile(t, "en", map[string]string{
		"plugins/github/ui.toml": "[noun]\none = \"x\"\n",
		"decisions.toml":         "[kinds.answer]\nnoun = { one = \"x\" }\n",
	})
	for _, field := range []string{"noun", "kinds.answer.noun"} {
		if _, ok := problemAt(problems, field, "needs other"); !ok {
			t.Errorf("%s: %v", field, problems)
		}
	}
}

func TestViewNamedHomeIsRefused(t *testing.T) {
	l, problems := loadProfile(t, "en", map[string]string{"views.toml": "[views.home]\nlabel = \"Home\"\nsummary = \"x\"\nwhen = \"true\"\n"})
	if _, ok := problemAt(problems, "views.home", "home route"); !ok {
		t.Fatalf("problems: %v", problems)
	}
	if _, ok := l.Views.View("home"); ok {
		t.Fatal("a view named home loaded")
	}
}

func TestBundledDecisionViewsCoverEveryKind(t *testing.T) {
	l, _ := loadProfile(t, "en", nil)
	for _, k := range decision.Kinds {
		t.Run(string(k), func(t *testing.T) {
			v, ok := l.Views.View(l.Decisions.Kinds[k].View)
			if !ok || !strings.Contains(v.WhenText, "task.decisions") || v.Rail {
				t.Fatalf("view %q of %s: %+v", l.Decisions.Kinds[k].View, k, v)
			}
		})
	}
	for _, id := range []string{"needs-me", "inbox"} {
		if v, _ := l.Views.View(id); !v.Rail {
			t.Errorf("%s left the rail", id)
		}
	}
}
