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
line = "x"
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

func TestTurnStartRefusesARunnerItsIntentDoesNotList(t *testing.T) {
	root := t.TempDir()
	writeWorkflow(t, root, "review-headless", `summary = "Case"
provider = "github"
when = 'pr.draft'

[[step]]
use = "turn.start"
intent = "ask-author"
runner = "headless"
`)
	writeIntent(t, root, "ask-author", minimal, "Ask.")
	m := strings.ReplaceAll(strings.Replace(minimal, `workspace = "review"`, "workspace = \"given\"\nprovider = \"slack\"", 1), "{{.pr.slug}}", "{{.unit.ref}}")
	writeIntent(t, root, "ask-owner", m, "Ask.")
	writeWorkflow(t, root, "ask-headless", strings.Replace(slackTurn, `intent = "act"`, `intent = "ask-owner"`, 1)+"\n[limits]\nper_day = 3\n")
	_, problems := Load(userLayer(root), nil, testBase(), nil)
	for _, want := range []string{
		`step[0].runner: the intent "ask-author" does not run as headless; its runners are session`,
		`step[0].runner: the intent "ask-owner" does not run as headless; its runners are session`,
	} {
		if !strings.Contains(problems.Error(), want) {
			t.Errorf("want %q in\n%s", want, problems.Error())
		}
	}
}
