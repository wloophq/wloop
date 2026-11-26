package intent_test

import (
	"slices"
	"testing"

	"github.com/wloophq/wloop/internal/intent"
)

// TestAUserLayerWordsOneSourceValueAndKeepsThePluginsOthers checks that a layer's values of a facet join the
// lower layers' by value, so a plugin layer or the user's words one value without dropping another layer's.
func TestAUserLayerWordsOneSourceValueAndKeepsThePluginsOthers(t *testing.T) {
	l, problems := loadProfile(t, "en", map[string]string{"views.toml": "[facets.source.values]\ngithub = \"Hub\"\n"})
	if len(problems) != 0 {
		t.Fatalf("problems %v", problems)
	}
	i := slices.IndexFunc(l.Views.Facets, func(f intent.FacetDef) bool { return f.ID == "source" })
	if i < 0 {
		t.Fatal("no source facet")
	}
	if w := l.Views.Facets[i].Values; w["github"] != "Hub" || w["slack"] != "Slack" || w["session"] != "Session" {
		t.Errorf("task source words %v", w)
	}
}
