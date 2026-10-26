package cli

import (
	"slices"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/brief"
	"github.com/wloophq/wloop/internal/workspace/fake"
)

func TestTheBriefQueueReadsTheProfilesBriefIntent(t *testing.T) {
	te := newTestEnv(t, &fake.Manager{})
	var logs []string
	logf := func(f string, a ...any) { logs = append(logs, f) }
	if bi := te.Env.briefIntent(logf)(); bi.Schema.Extra != nil || bi.Prompt == "" || bi.Schema.JSON() != brief.Schema {
		t.Fatalf("the bundled intent: %+v", bi)
	}
	userProfile(t, te, map[string]string{
		"intents/our-brief/intent.toml": `label = "Our brief"
summary = "The brief with the approach"
builds = "brief"
provider = ["github", "slack"]
prompt = "prompts/brief.md"
schema = "schema.json"
`,
		"intents/our-brief/schema.json": `{"type":"object","properties":{"approach":{"type":"string","enum":["patch","rewrite"]}}}`,
		"prompts/brief.md":              "Our own brief prompt {{title}}",
	})
	bi := te.Env.briefIntent(logf)()
	if bi.Schema.Extra == nil || !slices.Equal(bi.Schema.Missing(brief.Brief{}), []string{"approach"}) || !strings.HasPrefix(bi.Prompt, "Our own") ||
		bi.FieldsPrompt == "" || len(logs) != 0 {
		t.Fatalf("the user's intent: %+v, logs %v", bi, logs)
	}
}
