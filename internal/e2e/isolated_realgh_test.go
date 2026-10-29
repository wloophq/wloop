package e2e

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/cli"
	"github.com/wloophq/wloop/internal/config"
)

func TestIsolatedSetupRealGhScopesGitHubAndLeavesGhUnstubbed(t *testing.T) {
	bin := fakeDaemon(t)
	plugin := filepath.Join(filepath.Dir(bin), "plugins", "github")
	if err := os.MkdirAll(plugin, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(plugin, "plugin.toml"), []byte("id = \"github\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	parent := shortDir(t)
	for i, bad := range []string{"acme", "acme/api;x", "acme/api/extra"} {
		if out, err := isolated(t, "setup", filepath.Join(parent, "bad"+string(rune('a'+i))), bin, isolatedDM, "3", "--real-gh", bad); err == nil {
			t.Errorf("--real-gh %q: %s", bad, out)
		}
	}
	dir := filepath.Join(parent, "gh")
	mustIsolated(t, "setup", dir, bin, isolatedDM, "3", "--real-gh", "acme/api,acme/web,other/tool")
	t.Cleanup(func() { isolated(t, "stop", dir) })

	c, err := config.LoadFile(filepath.Join(dir, "config", brand.StateDir(), "config.toml"), cli.Defaults())
	if err != nil {
		t.Fatal(err)
	}
	if !c.PluginEnabled("github") || c.PluginTrust("github") != "path" {
		t.Errorf("github: enabled %v trust %q", c.PluginEnabled("github"), c.PluginTrust("github"))
	}
	if want := []string{"acme", "other"}; !slices.Equal(c.GitHub.Owners, want) {
		t.Errorf("owners %q, want %q", c.GitHub.Owners, want)
	}
	if want := []string{"acme/api", "acme/web", "other/tool"}; !slices.Equal(c.GitHub.Scope.Repos, want) {
		t.Errorf("scope repos %q, want %q", c.GitHub.Scope.Repos, want)
	}
	if _, err := os.Stat(filepath.Join(dir, "bin", "gh")); !os.IsNotExist(err) {
		t.Errorf("--real-gh still stubs gh: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "bin", "orca")); err != nil {
		t.Errorf("--real-gh dropped the orca stub: %v", err)
	}
	out, err := isolatedEnv(t, []string{"GH_TOKEN=", "GH_CONFIG_DIR="}, "run", dir, "sh", "-c", `echo "$GH_TOKEN|$GH_CONFIG_DIR"`)
	if err != nil || out != "|\n" {
		t.Errorf("gh's environment %q, %v: want the user's own", out, err)
	}
	if exports, err := isolated(t, "env", dir); err != nil || strings.Contains(exports, "GH_TOKEN") || strings.Contains(exports, "GH_CONFIG_DIR") {
		t.Errorf("env:\n%s", exports)
	}
}

func TestIsolatedSetupSlackTrustsThePluginByItsPath(t *testing.T) {
	dir := setupIsolated(t, shortDir(t), "slack", 4, "--slack")
	c, err := config.LoadFile(filepath.Join(dir, "config", brand.StateDir(), "config.toml"), cli.Defaults())
	if err != nil {
		t.Fatal(err)
	}
	if !c.PluginEnabled("slack") || c.PluginTrust("slack") != "path" {
		t.Errorf("slack: enabled %v trust %q", c.PluginEnabled("slack"), c.PluginTrust("slack"))
	}
	if c.Slack.Ingest.Socket != nil && !*c.Slack.Ingest.Socket {
		t.Error("--slack turned Slack's socket off")
	}
}
