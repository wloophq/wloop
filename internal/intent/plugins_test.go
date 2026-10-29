package intent

import (
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"testing/fstest"

	"github.com/wloophq/wloop/internal/config"
	dunit "github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/workflow"
	"github.com/wloophq/wloop/internal/plugins/bundled"
	"github.com/wloophq/wloop/internal/profile"
	"github.com/wloophq/wloop/profiles"
)

const quietSessions = `summary = "Close quiet sessions"
provider = "session"
when = 'session.managed'

[[step]]
use = "session.close"
`

// withBundledTasks is fsys with the bundled tasks.toml at its root, so the
// plugins' task workflows read its properties, and the chat intent it names.
func withBundledTasks(t *testing.T, fsys fs.FS) fs.FS {
	t.Helper()
	m := fstest.MapFS{}
	copyInto := func(src fs.FS, keep func(string) bool) {
		err := fs.WalkDir(src, ".", func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !keep(path) {
				return err
			}
			data, err := fs.ReadFile(src, path)
			m[path] = &fstest.MapFile{Data: data}
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	copyInto(bundledDefault(t), func(path string) bool { return path == TasksFile })
	copyInto(fsys, func(string) bool { return true })
	return withChat(t, m)
}

// usePlugins registers ps for the rest of t, as the composition root does.
func usePlugins(t *testing.T, ps ...profile.PluginDir) {
	profile.UsePlugins(ps)
	t.Cleanup(func() { profile.UsePlugins(nil) })
}

func workflowNames(l *Loaded) []string {
	var out []string
	for _, w := range l.Workflows {
		out = append(out, w.Name)
	}
	return out
}

func TestAPluginsProfileLayerLoadsOnlyWhenEnabled(t *testing.T) {
	plugin, user := t.TempDir(), t.TempDir()
	writeWorkflow(t, filepath.Join(plugin, profile.PluginProfileDir), "acme-quiet", quietSessions)
	writeWorkflow(t, filepath.Join(user, profile.PluginsDir, "acme"), "acme-mine", quietSessions)
	usePlugins(t, profile.PluginDir{ID: "acme", Dir: plugin})
	off := false
	for _, c := range []struct {
		name string
		sw   map[string]config.Plugin
		want bool
	}{
		{"enabled", nil, true},
		{"disabled", map[string]config.Plugin{"acme": {Enabled: &off}}, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			cfg := config.Config{Profile: user, Plugins: config.Plugins{Other: c.sw}}
			l, problems := Load(profile.Chain(cfg), withBundledTasks(t, profiles.Default), testBase(), nil)
			if len(problems) > 0 {
				t.Fatalf("%s", problems.Error())
			}
			names := workflowNames(l)
			for _, w := range []string{"acme-quiet", "acme-mine"} {
				if slices.Contains(names, w) != c.want {
					t.Errorf("workflow %s loaded: %t, want %t", w, !c.want, c.want)
				}
			}
		})
	}
}

func TestADisabledPluginsKeysStillParse(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "config.toml")
	raw := "workspace_managers = [\"orca\"]\n[plugins.github]\nenabled = false\nowners = [\"acme\"]\n[plugins.github.scope]\nrepos = [\"acme/api\"]\n"
	if err := os.WriteFile(file, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.LoadFile(file, config.Defaults())
	if err != nil {
		t.Fatalf("a disabled plugin's settings: %v", err)
	}
	if cfg.PluginEnabled("github") || !slices.Equal(cfg.GitHub.Owners, []string{"acme"}) {
		t.Errorf("enabled %t, owners %v", cfg.PluginEnabled("github"), cfg.GitHub.Owners)
	}

	user := t.TempDir()
	writeWorkflow(t, user, "acme-stale", "summary = \"Stale\"\nprovider = \"github\"\nwhen = 'pr.state == \"OPEN\" && pr.silent_days > 3'\n\n[[step]]\nuse = \"notify.self\"\n")
	if err := os.WriteFile(filepath.Join(user, "workflows", "acme-stale", "text.md.tmpl"), []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg.Profile = user
	l, problems := Load(profile.Chain(cfg), withBundledTasks(t, profiles.Default), testBase(), nil)
	if len(problems) > 0 || !slices.Contains(workflowNames(l), "acme-stale") {
		t.Fatalf("a profile reading a disabled plugin's facts: %v", problems)
	}

	codecs, err := bundled.Codecs()
	if err != nil {
		t.Fatal(err)
	}
	reg := dunit.NewRegistry(codecs[dunit.GitHub])
	u, err := reg.Parse("github", "acme/api#7")
	if err != nil || u.Label() != "acme/api#7" {
		t.Errorf("a disabled plugin's unit key: %v, %q", err, u.Label())
	}
	if got, ok := reg.ParseURL("https://github.com/acme/api/pull/7"); !ok || got != u {
		t.Errorf("a disabled plugin's address: %v %t", got, ok)
	}
}

func TestTheBundledSlackLayerLoadsWithNoPluginDirectory(t *testing.T) {
	usePlugins(t)
	off := false
	slackWorkflows := []string{"slack-ask-owner", "slack-closure", "slack-stalled-nudge"}
	for _, c := range []struct {
		name string
		on   *bool
		want bool
	}{
		{"enabled", nil, true},
		{"disabled", &off, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			var cfg config.Config
			cfg.Plugins.Slack.Enabled = c.on
			l, problems := Load(profile.Chain(cfg), bundledFS(), testBase(), nil)
			if len(problems) > 0 {
				t.Fatalf("%s", problems.Error())
			}
			names := workflowNames(l)
			for _, w := range slackWorkflows {
				if slices.Contains(names, w) != c.want {
					t.Errorf("workflow %s loaded: %t, want %t", w, !c.want, c.want)
				}
			}
			tools, problems := loadTools(profile.Chain(cfg), os.DirFS("../../profiles/default"))
			if len(problems) > 0 {
				t.Fatalf("%s", problems.Error())
			}
			if _, ok := tools["slack_read"]; ok != c.want {
				t.Errorf("slack_read worded: %t, want %t", ok, c.want)
			}
		})
	}
}

func TestAUserOverridesAWorkflowOfAPluginsLayer(t *testing.T) {
	usePlugins(t)
	user := t.TempDir()
	dir := filepath.Join(user, "workflows", "slack-closure")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "text.md.tmpl"), []byte("Closing."), 0o644); err != nil {
		t.Fatal(err)
	}
	l, problems := Load(profile.Chain(config.Config{Profile: user}), bundledFS(), testBase(), nil)
	if len(problems) > 0 {
		t.Fatalf("%s", problems.Error())
	}
	i := slices.IndexFunc(l.Workflows, func(w workflow.Workflow) bool { return w.Name == "slack-closure" })
	if i < 0 || !slices.Equal(l.Workflows[i].Overrides, []string{"text.md.tmpl"}) {
		t.Fatalf("slack-closure %+v", l.Workflows)
	}
}
