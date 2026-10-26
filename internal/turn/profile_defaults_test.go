package turn

import (
	"os"
	"path/filepath"
	"testing"

	dintent "github.com/wloophq/wloop/internal/domain/intent"
	"github.com/wloophq/wloop/internal/domain/unit"
	intentload "github.com/wloophq/wloop/internal/intent"
	"github.com/wloophq/wloop/internal/intent/builtin"
	"github.com/wloophq/wloop/internal/profile"
)

const sumUp = `label = "Sum up"
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

func TestALayersDefaultProviderFillsOnlyTheManifestsThatNameNone(t *testing.T) {
	dir := t.TempDir()
	at := filepath.Join(dir, "intents", "sum-up")
	if err := os.MkdirAll(at, 0o700); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{
		filepath.Join(dir, intentload.DefaultsFile): "provider = \"slack\"\n",
		filepath.Join(at, "intent.toml"):            sumUp,
		filepath.Join(at, "instructions.md.tmpl"):   "Sum up {{.Wording.This}}.\n",
	} {
		if err := os.WriteFile(name, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	b, err := Base()
	if err != nil {
		t.Fatal(err)
	}
	l, problems := intentload.Load([]profile.Layer{{Name: "user", Source: dir, Dir: dir}}, BundledFS(), b, CheckTemplate)
	if len(problems) > 0 {
		t.Fatalf("%s", problems.Error())
	}
	for _, role := range []string{builtin.RoleReview, builtin.RoleAddress, builtin.RoleNudge, builtin.RoleProbe} {
		if _, ok := l.Registry.ByRole(role, unit.GitHub); !ok {
			t.Errorf("%s has no github holder", role)
		}
	}
	for _, name := range []dintent.Name{dintent.Review, dintent.Address, dintent.Nudge, dintent.Probe} {
		if d, _ := l.Registry.Get(name); d.Provider != unit.GitHub {
			t.Errorf("the bundled %s runs on %q", name, d.Provider)
		}
	}
	if d, _ := l.Registry.Get("sum-up"); d.Provider != "slack" {
		t.Errorf("sum-up runs on %q, want the layer's default", d.Provider)
	}
}
