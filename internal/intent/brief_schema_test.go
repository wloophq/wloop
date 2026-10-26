package intent

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const briefBuilder = `label = "Brief"
summary = "One brief per unit"
builds = "brief"
provider = ["github", "slack"]
prompt = "prompts/brief.md"
schema = "schema.json"
`

const onApproach = `summary = "Case"
provider = "github"
when = 'brief.extra.approach == "rewrite"'

[[step]]
use = "turn.start"
intent = "ask-author"
`

func TestAWorkflowReadsTheBriefIntentsExtras(t *testing.T) {
	setup := func(t *testing.T, schema string) string {
		root := t.TempDir()
		writeIntent(t, root, "ask-author", minimal, "{{.Headline}}")
		dir := filepath.Join(root, "intents", "our-brief")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		for name, body := range map[string]string{"intent.toml": briefBuilder, "schema.json": schema} {
			if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		writeWorkflow(t, root, "on-rewrite", onApproach)
		return root
	}
	l, problems := Load(userLayer(setup(t, `{"type":"object","properties":{"approach":{"type":"string","enum":["patch","rewrite"]}}}`)), bundledFS(), testBase(), nil)
	if len(problems) > 0 || len(l.Workflows) != 1 {
		t.Fatalf("%d workflows: %v", len(l.Workflows), problems)
	}
	l, _ = Load(userLayer(setup(t, `{"type":"object","properties":{"solution":{"type":"string"}}}`)), bundledFS(), testBase(), nil)
	if got := l.BrokenWorkflows["on-rewrite"]; !strings.Contains(got.Error(), "brief.extra.approach") {
		t.Fatalf("an undeclared extra: %v", got)
	}
}

func TestABriefSchemaFieldNoFactCanHoldIsRefused(t *testing.T) {
	for field, prop := range map[string]string{
		"score": `{"type":"number"}`,
		"none":  `{"type":"null"}`,
		"sizes": `{"type":"array","items":{"type":"integer"}}`,
		"flags": `{"type":"array","items":{"type":"boolean"}}`,
	} {
		root := t.TempDir()
		dir := filepath.Join(root, "intents", "our-brief")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		schema := `{"type":"object","properties":{"approach":{"type":"string"},"` + field + `":` + prop + `}}`
		for name, body := range map[string]string{"intent.toml": briefBuilder, "schema.json": schema} {
			if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		_, problems := Load(userLayer(root), bundledFS(), testBase(), nil)
		if !strings.Contains(fmt.Sprint(problems), "our-brief") || !strings.Contains(fmt.Sprint(problems), "$."+field+":") {
			t.Errorf("%s %s: %v", field, prop, problems)
		}
	}
}
