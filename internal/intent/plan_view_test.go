package intent_test

import (
	"strings"
	"testing"
)

func TestViewNamedPlanIsRefused(t *testing.T) {
	l, problems := loadProfile(t, "en", map[string]string{"views.toml": "[views.plan]\nlabel = \"Plan\"\nsummary = \"x\"\nwhen = \"true\"\n"})
	if _, ok := problemAt(problems, "views.plan", "plan route"); !ok {
		t.Fatalf("problems: %v", problems)
	}
	if _, ok := l.Views.View("plan"); ok {
		t.Fatal("a view named plan loaded")
	}
}

func TestAPluginLayerWordingThePlanNamesWhereItsWordsMoved(t *testing.T) {
	_, problems := loadProfile(t, "en", map[string]string{"plugins/github/ui.toml": "[views.plan]\nlabel = \"Runs\"\n"})
	p, ok := problemAt(problems, "views.plan", "strings.toml")
	if !ok || !strings.Contains(p.Msg, `"Plan"`) {
		t.Fatalf("problems: %v", problems)
	}
}

func TestAnObsoletePlanViewKeepsTheRestOfThePluginLayer(t *testing.T) {
	l, problems := loadProfile(t, "en", map[string]string{"plugins/github/ui.toml": "[views.plan]\nlabel = \"Runs\"\n\n[views.mine]\nlabel = \"Mine\"\n"})
	if _, ok := problemAt(problems, "views.plan", "strings.toml"); !ok {
		t.Fatalf("problems: %v", problems)
	}
	if got := l.UI["github"].Views["mine"].Label; got != "Mine" {
		t.Errorf("the layer's other words read %q, want the user's Mine", got)
	}
}

func TestTheBundledPluginsWordNoPlanView(t *testing.T) {
	l, problems := loadProfile(t, "en", nil)
	if len(problems) > 0 {
		t.Fatal(problems)
	}
	for id, u := range l.UI {
		if _, ok := u.Views["plan"]; ok {
			t.Errorf("plugin %s words the plan view, which the core declares", id)
		}
	}
}
