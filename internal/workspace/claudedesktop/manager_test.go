package claudedesktop

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/workspace"
)

func TestRewritePrompt(t *testing.T) {
	m := &Manager{}
	for in, want := range map[string]string{
		"/acme-review https://github.com/acme/api/pull/7": "Invoke the skill acme-review with the arguments: https://github.com/acme/api/pull/7",
		"/acme-nudge  url\n  more":                        "Invoke the skill acme-nudge with the arguments: url more",
		"/compact":                                        "Invoke the skill compact.",
		"review this":                                     "review this",
		"/ not a command":                                 "/ not a command",
		"//double":                                        "//double",
		"/usr/bin/env is a path":                          "/usr/bin/env is a path",
		"/plugin:skill a":                                 "Invoke the skill plugin:skill with the arguments: a",
	} {
		if got := m.RewritePrompt(in); got != want {
			t.Errorf("RewritePrompt(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestHonorsOnlyTheDefaultLaunch(t *testing.T) {
	m := &Manager{}
	if err := m.Honors(workspace.LaunchNeeds{}); err != nil {
		t.Fatal(err)
	}
	for needs, word := range map[workspace.LaunchNeeds]string{
		{UserSettingsOnly: true}: "own settings",
		{Strict: true}:           "only permissions",
		{NoMCP: true}:            "MCP",
		{CustomHome: true}:       "default Claude home",
	} {
		if err := m.Honors(needs); err == nil || !strings.Contains(err.Error(), word) {
			t.Errorf("Honors(%+v) = %v, want it to name %q", needs, err, word)
		}
	}
}

func TestMergeSettingsIsAdditive(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".claude", "settings.local.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	have := `{"model":"sonnet","permissions":{"allow":["Bash(ls)"],"defaultMode":"acceptEdits"}}`
	if err := os.WriteFile(path, []byte(have), 0o600); err != nil {
		t.Fatal(err)
	}
	add := `{"model":"opus","disableAllHooks":true,"permissions":{"allow":["Bash(ls)","mcp__acme__turn_end"],"deny":["Edit(/x/.git/**)"],"defaultMode":"plan"}}`
	if err := mergeSettings(dir, add); err != nil {
		t.Fatal(err)
	}
	var got struct {
		Model           string `json:"model"`
		DisableAllHooks bool   `json:"disableAllHooks"`
		Permissions     struct {
			Allow       []string `json:"allow"`
			Deny        []string `json:"deny"`
			DefaultMode string   `json:"defaultMode"`
		} `json:"permissions"`
	}
	b, _ := os.ReadFile(path)
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if got.Model != "sonnet" || !got.DisableAllHooks || got.Permissions.DefaultMode != "acceptEdits" ||
		!slices.Equal(got.Permissions.Allow, []string{"Bash(ls)", "mcp__acme__turn_end"}) ||
		!slices.Equal(got.Permissions.Deny, []string{"Edit(/x/.git/**)"}) {
		t.Fatalf("merged = %s", b)
	}
	if err := mergeSettings(dir, ""); err != nil {
		t.Fatal(err)
	}
	if err := mergeSettings(dir, "[1]"); err == nil {
		t.Fatal("merged settings that are no object")
	}
}

func TestSessionEnv(t *testing.T) {
	root := t.TempDir()
	env := SessionEnv{Root: root}
	vars := map[string]string{EntrypointVar: "claude-desktop", HandleVar: HandleOf(testUUID)}
	getenv := func(k string) string { return vars[k] }
	if env.Handle(getenv) != HandleOf(testUUID) || env.Pane(getenv) != HandleOf(testUUID) || env.WorktreeID(getenv) != "" {
		t.Fatal("Desktop session not read")
	}
	vars[EntrypointVar] = "cli"
	if env.Handle(getenv) != "" {
		t.Fatal("a CLI session read as Desktop's")
	}
	if !env.Claims(filepath.Join(root, "api", "w")) || !env.Claims(filepath.Join(root, "api", "w", "sub")) {
		t.Fatal("a worktree under the root is not claimed")
	}
	if env.Claims(root) || env.Claims(filepath.Dir(root)) || env.Claims(root+"x/api/w") || (SessionEnv{}).Claims(root) {
		t.Fatal("claimed a directory outside the root")
	}
}

func TestControlPaths(t *testing.T) {
	if got := ControlPaths("/u"); !slices.Equal(got, []string{"/u/.claude/sessions", "/tmp/cc-socks"}) {
		t.Fatalf("ControlPaths = %v", got)
	}
}
