package intent_test

import (
	"strings"
	"testing"
)

func TestViewNamedRecentIsRefused(t *testing.T) {
	l, problems := loadProfile(t, "en", map[string]string{"views.toml": "[views.recent]\nlabel = \"Recent\"\nsummary = \"x\"\nwhen = \"true\"\n"})
	if _, ok := problemAt(problems, "views.recent", "recent route"); !ok {
		t.Fatalf("problems: %v", problems)
	}
	if _, ok := l.Views.View("recent"); ok {
		t.Fatal("a view named recent loaded")
	}
}

func TestAPluginLayerWordingRecentNamesWhereItsWordsMoved(t *testing.T) {
	l, problems := loadProfile(t, "en", map[string]string{"plugins/github/ui.toml": "[views.recent]\nlabel = \"Latest\"\n\n[views.mine]\nlabel = \"Mine\"\n"})
	p, ok := problemAt(problems, "views.recent", "strings.toml")
	if !ok || !strings.Contains(p.Msg, `"Recent"`) {
		t.Fatalf("problems: %v", problems)
	}
	if got := l.UI["github"].Views["mine"].Label; got != "Mine" {
		t.Errorf("the layer's other words read %q, want the user's Mine", got)
	}
}

func TestTheBundledPluginsWordNoRecentView(t *testing.T) {
	l, problems := loadProfile(t, "en", nil)
	if len(problems) > 0 {
		t.Fatal(problems)
	}
	for id, u := range l.UI {
		if _, ok := u.Views["recent"]; ok {
			t.Errorf("plugin %s words the recent view, which the core declares", id)
		}
	}
}
