package brief

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	intentload "github.com/wloophq/wloop/internal/intent"
	"github.com/wloophq/wloop/internal/profile"
	"github.com/wloophq/wloop/internal/turn"
)

func loadIntents(t *testing.T, files map[string]string) (*intentload.Loaded, intentload.Problems) {
	t.Helper()
	dir := t.TempDir()
	for rel, body := range files {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	base, err := turn.Base()
	if err != nil {
		t.Fatal(err)
	}
	return intentload.Load([]profile.Layer{{Name: "user", Source: dir, Dir: dir}}, turn.BundledFS(), base, turn.CheckTemplate)
}

const userBrief = `label = "Our brief"
summary = "The brief with how each change solves its goal"
user_facing = false
builds = "brief"
provider = ["github", "slack"]
prompt = "prompts/brief.md"
schema = "schema.json"
`

func TestTheBriefIntentIsFoundByBuilds(t *testing.T) {
	l, problems := loadIntents(t, nil)
	if len(problems) > 0 {
		t.Fatal(problems)
	}
	for _, p := range []string{"github", "slack"} {
		d, ok := l.Registry.Builder("brief", "github")
		if !ok || !d.Bundled || d.Prompt != PromptFile || len(d.Extra) != 0 {
			t.Fatalf("%s: the bundled builder %+v %v", p, d, ok)
		}
	}
	l, problems = loadIntents(t, map[string]string{
		"intents/our-brief/intent.toml": userBrief,
		"intents/our-brief/schema.json": extraSchema,
	})
	if len(problems) > 0 {
		t.Fatal(problems)
	}
	d, ok := l.Registry.Builder("brief", "slack")
	if !ok || d.Name != "our-brief" || len(d.Extra) != 2 {
		t.Fatalf("the user's builder %+v %v", d, ok)
	}
	c, err := ComposeExtra(Schema, d.Extra)
	if err != nil || !slices.Equal(c.Missing(Brief{}), []string{"approach", "solution"}) {
		t.Fatalf("composed from the intent: %v %v", c.Missing(Brief{}), err)
	}
	if want := composed(t).FieldVersions(); c.FieldVersions()["approach"] != want["approach"] {
		t.Errorf("the intent's field versions %v, the schema file's %v", c.FieldVersions(), want)
	}
	for name, files := range map[string]map[string]string{
		"two builders in one layer": {
			"intents/our-brief/intent.toml":  userBrief,
			"intents/our-brief/schema.json":  extraSchema,
			"intents/your-brief/intent.toml": strings.Replace(userBrief, `"Our brief"`, `"Your brief"`, 1),
			"intents/your-brief/schema.json": extraSchema,
		},
		"a nested object": {
			"intents/our-brief/intent.toml": userBrief,
			"intents/our-brief/schema.json": `{"type":"object","properties":{"plan":{"type":"object","properties":{}}}}`,
		},
		"no schema file": {"intents/our-brief/intent.toml": userBrief},
	} {
		_, problems := loadIntents(t, files)
		want := map[string]string{"two builders in one layer": "one builder per provider", "a nested object": "$.plan: a nested object",
			"no schema file": "schema.json is not in the intent's directory"}[name]
		if !strings.Contains(problems.Error(), want) {
			t.Errorf("%s: %v", name, problems)
		}
	}
}
