package cli

import (
	"os"
	"path/filepath"
	"testing"
)

const threadIntent = `label = "Sum up"
summary = "Sum up a thread for its owner"
workspace = "pr_else_main"
session = { strategy = "new", title = "sumup" }

[headline]
resumed = "sum up {{.Wording.Ref}}"
[[headline.new]]
text = "Sum up {{.Wording.Ref}}, {{.Who}}."

[record]
states = ["done"]
`

func TestAProfileDefaultProviderLeavesTheBundledIntentsTheirOwn(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, nil)
	dir := t.TempDir()
	at := filepath.Join(dir, "intents", "sum-up")
	if err := os.MkdirAll(at, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{
		filepath.Join(dir, "defaults.toml"):      "provider = \"slack\"\n",
		filepath.Join(at, "intent.toml"):          threadIntent,
		filepath.Join(at, "instructions.md.tmpl"): "Sum up {{.Wording.This}}.\n",
	} {
		if err := os.WriteFile(name, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if code := te.run("profile", "check", dir); code != 0 {
		t.Fatalf("code %d\n%s%s", code, te.out.String(), te.err.String())
	}
}
