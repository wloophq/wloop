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
