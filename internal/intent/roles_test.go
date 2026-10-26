package intent

import (
	"strings"
	"testing"
)

func TestReconcileStaysWithTheIntentsThatTakeABundledRole(t *testing.T) {
	root := t.TempDir()
	writeIntent(t, root, "implement", strings.Replace(minimal, `workspace = "review"`, "workspace = \"review\"\nreconcile = \"my_review\"", 1), "{{.Headline}}")
	_, problems := Load(userLayer(root), nil, testBase(), nil)
	if !strings.Contains(problems.Error(), "reconcile: is reserved for the bundled intents") {
		t.Fatalf("%s", problems.Error())
	}
}

func TestARoundOrNudgeIntentWithoutCapSpendsTheRoundBudget(t *testing.T) {
	for _, c := range []struct{ keys, kind string }{
		{`role = "round.review"`, "review"},
		{`role = "round.address"`, "address"},
		{`role = "nudge"`, ""},
		{`round_kind = "review"`, "review"},
	} {
		root := t.TempDir()
		writeIntent(t, root, "implement", strings.Replace(minimal, `workspace = "review"`, "workspace = \"review\"\n"+c.keys, 1), "{{.Headline}}")
		l, problems := Load(userLayer(root), nil, testBase(), nil)
		if len(problems) > 0 {
			t.Fatalf("%s: %s", c.keys, problems.Error())
		}
		d, _ := l.Registry.Get("implement")
		if d.RoundKind != c.kind || d.Cap != "rounds" {
			t.Errorf("%s: round_kind %q cap %q", c.keys, d.RoundKind, d.Cap)
		}
	}
}
