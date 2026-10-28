package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/config"
	"github.com/wloophq/wloop/internal/profile"
)

func TestAPluginsProfileReadsItsBundledLayerUnderTheUsers(t *testing.T) {
	profile.UsePlugins(nil)
	t.Cleanup(func() { profile.UsePlugins(nil) })
	const prompt = "prompts/slack-extraction.md"
	user := t.TempDir()
	cfg := config.Config{Profile: user}
	read := pluginProfile("slack", func() config.Config { return cfg })

	shipped, err := read(context.Background(), prompt)
	if err != nil || !strings.Contains(string(shipped), "{{") {
		t.Fatalf("the bundled layer's prompt: %v %.40q", err, shipped)
	}
	if _, err := read(context.Background(), "tools.toml"); err != nil {
		t.Errorf("the bundled layer's tools.toml: %v", err)
	}

	mine := filepath.Join(user, profile.PluginsDir, "slack", "prompts")
	if err := os.MkdirAll(mine, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mine, "slack-extraction.md"), []byte("my prompt"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := read(context.Background(), prompt); err != nil || string(got) != "my prompt" {
		t.Errorf("the user's prompt: %v %q", err, got)
	}
}
