package turn

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/pelletier/go-toml/v2"

	dintent "github.com/wloophq/wloop/internal/domain/intent"
	"github.com/wloophq/wloop/internal/domain/unit"
	intentload "github.com/wloophq/wloop/internal/intent"
	"github.com/wloophq/wloop/internal/intent/builtin"
	"github.com/wloophq/wloop/internal/profile"
)

const sumUp = `label = "Sum up"
summary = "Sum up a thread for its owner"
workspace = "linked_else_main"
session = { strategy = "new", title = "sumup" }

[headline]
resumed = "sum up {{.unit.ref}}"
[[headline.new]]
line = 'Sum up {{.unit.ref}}, {{template "started_by" .}}.'

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

func TestEveryBundledIntentNamesItsProvider(t *testing.T) {
	manifests, err := fs.Glob(BundledFS(), "intents/*/intent.toml")
	if err != nil || len(manifests) == 0 {
		t.Fatalf("no bundled manifests: %v", err)
	}
	for _, m := range manifests {
		raw, err := fs.ReadFile(BundledFS(), m)
		if err != nil {
			t.Fatal(err)
		}
		var keys map[string]any
		if err := toml.Unmarshal(raw, &keys); err != nil {
			t.Fatal(err)
		}
		if _, ok := keys["provider"]; !ok {
			t.Errorf("%s names no provider, so a layer's defaults.toml would move it", m)
		}
	}
}
