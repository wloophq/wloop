package intent

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestTurnStartRefusesABuilderIntent(t *testing.T) {
	root := t.TempDir()
	writeIntent(t, root, "unit-brief", `label = "Brief"
summary = "One brief per unit"
builds = "brief"
prompt = "prompts/brief.md"
schema = "schema.json"

[headline]
resumed = "x"
[[headline.new]]
text = "x"
`, "{{.Headline}}")
	dir := filepath.Join(root, "intents", "unit-brief")
	writeFile(t, filepath.Join(dir, "schema.json"), `{"type": "object", "properties": {}}`)
	writeFile(t, filepath.Join(dir, "prompts", "brief.md"), "Brief {{marker}}")
	writeWorkflow(t, root, "brief-everything", `summary = "Case"
provider = "github"
when = 'pr.state == "open"'

[[step]]
use = "turn.start"
intent = "unit-brief"
`)
	l, problems := Load(userLayer(root), nil, testBase(), nil)
	if !strings.Contains(problems.Error(), `step[0].intent: the intent "unit-brief" builds a brief and is never a turn`) {
		t.Fatalf("%s", problems.Error())
	}
	if _, ok := l.Registry.Builder("brief", "github"); !ok {
		t.Fatal("the builder itself did not load")
	}
}
