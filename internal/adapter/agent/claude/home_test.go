package claude

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/core/setup"
	"github.com/wloophq/wloop/internal/domain/shell"
)

var _ setup.AgentHome = Home{}

const (
	testURL = "http://127.0.0.1:47800/mcp"
	testKey = "k1"
)

// fakeMCP plays `claude mcp add-json` and `claude mcp remove` on the
// .claude.json the environment names, the way Claude Code does.
func fakeMCP(t *testing.T, userHome string, during func()) ShellFunc {
	return func(_ context.Context, _ string, env []string, _ string, bin string, args ...string) ([]byte, error) {
		if bin != "claude" || len(args) < 5 || args[0] != "mcp" || args[2] != "--scope" || args[3] != "user" {
			return nil, fmt.Errorf("unexpected call %s %q", bin, args)
		}
		path := filepath.Join(userHome, ".claude.json")
		for _, kv := range env {
			if dir, ok := strings.CutPrefix(kv, "CLAUDE_CONFIG_DIR="); ok {
				path = filepath.Join(dir, ".claude.json")
			}
		}
		doc := map[string]any{}
		if raw, err := os.ReadFile(path); err == nil {
			if err := json.Unmarshal(raw, &doc); err != nil {
				t.Fatal(err)
			}
		}
		servers, _ := doc["mcpServers"].(map[string]any)
		if servers == nil {
			servers = map[string]any{}
		}
		switch args[1] {
		case "add-json":
			if _, ok := servers[args[4]]; ok {
				return nil, errors.New("already exists")
			}
			var v any
			if err := json.Unmarshal([]byte(args[5]), &v); err != nil {
				return nil, err
			}
			servers[args[4]] = v
		case "remove":
			delete(servers, args[4])
		default:
			return nil, fmt.Errorf("unexpected call %q", args)
		}
		doc["mcpServers"] = servers
		raw, _ := json.Marshal(doc)
		if err := os.WriteFile(path, raw, 0o644); err != nil {
			t.Fatal(err)
		}
		if during != nil {
			during()
		}
		return nil, nil
	}
}

func testHome(t *testing.T, during func()) (Home, string) {
	user := t.TempDir()
	return Home{Command: "'/bin/" + brand.Name + "'", MCPURL: testURL, MCPKey: testKey, DefaultHome: filepath.Join(user, ".claude"), Sh: fakeMCP(t, user, during),
		Now: func() time.Time { return time.Date(2026, 10, 18, 9, 30, 0, 0, time.UTC) }}, t.TempDir()
}

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func problems(t *testing.T, h Home, home string) map[string]string {
	t.Helper()
	fs, err := h.Check(context.Background(), home)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, f := range fs {
		if f.Home != home {
			t.Errorf("finding of another home: %+v", f)
		}
		if f.Problem != "" && f.Fix == "" && !strings.HasPrefix(f.Entry, "Orca") {
			t.Errorf("finding without a fix: %+v", f)
		}
		out[f.Entry] = f.Problem
	}
	return out
}

func TestCheckFindsEveryMissingEntryWithoutWriting(t *testing.T) {
	h, home := testHome(t, nil)
	got := problems(t, h, home)
	for _, entry := range []string{"MCP server " + brand.Name, "hook Stop", "hook SessionStart", "hook UserPromptSubmit", "permission " + TurnEndTool} {
		if !strings.Contains(got[entry], "missing") {
			t.Errorf("%s: %q", entry, got[entry])
		}
	}
	if entries, _ := os.ReadDir(home); len(entries) != 0 {
		t.Fatalf("check wrote %v", entries)
	}
}

func TestCheckNamesAWrongURLAHookThatCanBlockAndAMissingPermission(t *testing.T) {
	h, home := testHome(t, nil)
	write(t, filepath.Join(home, ".claude.json"), `{"mcpServers":{"`+brand.Name+`":{"type":"http","url":"http://127.0.0.1:1/mcp","headers":{"X-Loop-Session":"${ORCA_TERMINAL_HANDLE}"}}}}`)
	write(t, filepath.Join(home, "settings.json"), `{"hooks":{
		"Stop":[{"hooks":[{"type":"command","command":"'/bin/`+brand.Name+`' hook stop","timeout":10}]}],
		"SessionStart":[{"hooks":[{"type":"command","command":"'/bin/`+brand.Name+`' hook session-start || true","timeout":10}]}],
		"UserPromptSubmit":[{"hooks":[{"type":"command","command":"curl -s localhost:$ORCA_AGENT_HOOK_PORT/hook"}]}]}}`)
	got := problems(t, h, home)
	if p := got["MCP server "+brand.Name]; !strings.Contains(p, "http://127.0.0.1:1/mcp") || !strings.Contains(p, testURL) {
		t.Errorf("url: %q", p)
	}
	if p := got["hook Stop"]; !strings.Contains(p, "|| true") {
		t.Errorf("stop: %q", p)
	}
	if p, ok := got["hook SessionStart"]; !ok || p != "" {
		t.Errorf("session-start: %q %v", p, ok)
	}
	if p := got["permission "+TurnEndTool]; !strings.Contains(p, "missing") {
		t.Errorf("permission: %q", p)
	}
	if p := got["Orca status hook"]; !strings.Contains(p, "UserPromptSubmit") {
		t.Errorf("orca: %q", p)
	}
}

func TestCheckNamesAMissingHeadersHelper(t *testing.T) {
	h, home := testHome(t, nil)
	write(t, filepath.Join(home, ".claude.json"), `{"mcpServers":{"`+brand.Name+`":{"type":"http","url":"`+testURL+`"}}}`)
	if p := problems(t, h, home)["MCP server "+brand.Name]; !strings.Contains(p, "headersHelper") {
		t.Fatalf("%q", p)
	}
}

func TestCheckNamesAMissingOrStaleKeyWithoutPrintingIt(t *testing.T) {
	h, home := testHome(t, nil)
	entry := func(headers string) {
		write(t, filepath.Join(home, ".claude.json"), `{"mcpServers":{"`+brand.Name+`":{"type":"http","url":"`+testURL+`","headersHelper":"'/bin/`+brand.Name+`' hook mcp-headers","headers":{`+headers+`}}}}`)
	}
	entry("")
	if p := problems(t, h, home)["MCP server "+brand.Name]; p != "header X-Loop-Key is missing" {
		t.Fatalf("missing: %q", p)
	}
	entry(`"X-Loop-Key":"old-secret"`)
	if p := problems(t, h, home)["MCP server "+brand.Name]; p != "header X-Loop-Key is stale" {
		t.Fatalf("stale: %q", p)
	}
	h.MCPKey = ""
	if p := problems(t, h, home)["MCP server "+brand.Name]; !strings.Contains(p, "no MCP key") || strings.Contains(p, "old-secret") {
		t.Fatalf("no key in state: %q", p)
	}
	h.MCPKey = testKey
	lines, err := h.Install(context.Background(), home, false)
	if err != nil || len(lines) == 0 {
		t.Fatalf("install over a stale key: %q %v", lines, err)
	}
	if p := problems(t, h, home)["MCP server "+brand.Name]; p != "" {
		t.Fatalf("after install: %q", p)
	}
}

func TestInstallThenCheckFindsNothing(t *testing.T) {
	h, home := testHome(t, nil)
	if _, err := h.Install(context.Background(), home, false); err != nil {
		t.Fatal(err)
	}
	for entry, p := range problems(t, h, home) {
		if p != "" {
			t.Errorf("%s: %s", entry, p)
		}
	}
	lines, err := h.Install(context.Background(), home, false)
	if err != nil || len(lines) != 0 {
		t.Fatalf("second install: %q %v", lines, err)
	}
}

func TestInstallBytesArePinned(t *testing.T) {
	h, home := testHome(t, nil)
	mcp := h.Sh
	var calls [][]string
	h.Sh = func(ctx context.Context, dir string, env []string, stdin, bin string, args ...string) ([]byte, error) {
		calls = append(calls, append(append([]string{}, env...), append([]string{bin}, args...)...))
		return mcp(ctx, dir, env, stdin, bin, args...)
	}
	if _, err := h.Install(context.Background(), home, false); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 0 {
		t.Fatalf("calls %q", calls)
	}
	mcpRaw, _ := os.ReadFile(filepath.Join(home, ".claude.json"))
	if want := `{
  "mcpServers": {
    "` + brand.Name + `": {
      "headers": {
        "X-Loop-Key": "k1"
      },
      "headersHelper": "'/bin/` + brand.Name + `' hook mcp-headers",
      "type": "http",
      "url": "http://127.0.0.1:47800/mcp"
    }
  }
}
`; string(mcpRaw) != want {
		t.Fatalf(".claude.json:\n%s", mcpRaw)
	}
	if fi, err := os.Stat(filepath.Join(home, ".claude.json")); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v %v", fi.Mode(), err)
	}
	raw, _ := os.ReadFile(filepath.Join(home, "settings.json"))
	var settings = `{
  "hooks": {
    "SessionStart": [
      {
        "hooks": [
          {
            "command": "'/bin/` + brand.Name + `' hook session-start || true",
            "timeout": 10,
            "type": "command"
          }
        ]
      }
    ],
    "Stop": [
      {
        "hooks": [
          {
            "command": "'/bin/` + brand.Name + `' hook stop || true",
            "timeout": 10,
            "type": "command"
          }
        ]
      }
    ],
    "UserPromptSubmit": [
      {
        "hooks": [
          {
            "command": "'/bin/` + brand.Name + `' hook prompt || true",
            "timeout": 10,
            "type": "command"
          }
        ]
      }
    ]
  },
  "permissions": {
    "allow": [
      "` + brand.ToolPrefix() + `turn_end",
      "` + brand.ToolPrefix() + `slack_send_message"
    ]
  }
}
`
	if string(raw) != settings {
		t.Fatalf("settings.json:\n%s", raw)
	}
}

func TestInstallWritesHooksThatCannotBlock(t *testing.T) {
	h, home := testHome(t, nil)
	if _, err := h.Install(context.Background(), home, false); err != nil {
		t.Fatal(err)
	}
	var s struct {
		Hooks map[string][]struct {
			Hooks []struct {
				Type, Command string
				Timeout       int
			}
		}
	}
	raw, _ := os.ReadFile(filepath.Join(home, "settings.json"))
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatal(err)
	}
	for event, want := range map[string]string{"Stop": "'/bin/" + brand.Name + "' hook stop || true", "SessionStart": "'/bin/" + brand.Name + "' hook session-start || true",
		"UserPromptSubmit": "'/bin/" + brand.Name + "' hook prompt || true"} {
		g := s.Hooks[event]
		if len(g) != 1 || len(g[0].Hooks) != 1 || g[0].Hooks[0].Command != want || g[0].Hooks[0].Type != "command" || g[0].Hooks[0].Timeout != 10 {
			t.Errorf("%s: %+v", event, g)
		}
	}
}

func TestInstallReplacesAnOldHookInPlace(t *testing.T) {
	h, home := testHome(t, nil)
	write(t, filepath.Join(home, "settings.json"), `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"echo mine"},{"type":"command","command":"/old/`+brand.Command+` hook stop"}]}]}}`)
	lines, err := h.Install(context.Background(), home, false)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(lines, func(l string) bool { return strings.HasPrefix(l, "changed hook Stop") }) {
		t.Errorf("lines %q", lines)
	}
	raw, _ := os.ReadFile(filepath.Join(home, "settings.json"))
	if strings.Contains(string(raw), "/old/"+brand.Name) || !strings.Contains(string(raw), "echo mine") || strings.Count(string(raw), "hook stop") != 1 {
		t.Fatalf("%s", raw)
	}
}

func TestRemoveKeepsAConcurrentEdit(t *testing.T) {
	var settings string
	edit := false
	h, home := testHome(t, func() {
		if edit {
			write(t, settings, `{"model":"opus","permissions":{"allow":["Bash(ls *)","`+TurnEndTool+`"]}}`)
		}
	})
	settings = filepath.Join(home, "settings.json")
	write(t, settings, `{"permissions":{"allow":["Bash(ls *)"]}}`)
	if _, err := h.Install(context.Background(), home, false); err != nil {
		t.Fatal(err)
	}
	edit = true
	if _, err := h.Remove(context.Background(), home, false); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(settings)
	if !strings.Contains(string(raw), `"model": "opus"`) || !strings.Contains(string(raw), "Bash(ls *)") || strings.Contains(string(raw), TurnEndTool) {
		t.Errorf("%s", raw)
	}
}

func TestInstallKeepsCommandsReadable(t *testing.T) {
	h, home := testHome(t, nil)
	write(t, filepath.Join(home, "settings.json"), `{"statusLine":{"command":"a && b > c"},"n":1.50}`)
	if _, err := h.Install(context.Background(), home, false); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(filepath.Join(home, "settings.json"))
	if !strings.Contains(string(raw), `"a && b > c"`) || !strings.Contains(string(raw), "1.50") {
		t.Fatalf("%s", raw)
	}
}

func TestDryRunWritesNothing(t *testing.T) {
	h, home := testHome(t, nil)
	h.Sh = func(context.Context, string, []string, string, string, ...string) ([]byte, error) {
		t.Error("dry run ran claude")
		return nil, nil
	}
	write(t, filepath.Join(home, "settings.json"), `{}`)
	lines, err := h.Install(context.Background(), home, true)
	if err != nil || len(lines) == 0 {
		t.Fatalf("%q %v", lines, err)
	}
	for _, l := range lines {
		if !strings.HasPrefix(l, "would ") {
			t.Errorf("line %q", l)
		}
	}
	if entries, _ := os.ReadDir(home); len(entries) != 1 {
		t.Fatalf("dry run wrote %v", entries)
	}
}

func TestInstallWritesThroughASymlinkedSettings(t *testing.T) {
	h, home := testHome(t, nil)
	target := filepath.Join(t.TempDir(), "settings.json")
	write(t, target, `{"model":"opus"}`)
	if err := os.Symlink(target, filepath.Join(home, "settings.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := h.Install(context.Background(), home, false); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Lstat(filepath.Join(home, "settings.json")); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("the link was replaced: %v", err)
	}
	if raw, _ := os.ReadFile(target); !strings.Contains(string(raw), TurnEndTool) {
		t.Fatalf("%s", raw)
	}
}

func TestDefaultHomeRegistersInTheUsersClaudeJSON(t *testing.T) {
	h, _ := testHome(t, nil)
	if _, err := h.Install(context.Background(), h.DefaultHome, false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(h.DefaultHome, ".claude.json")); !os.IsNotExist(err) {
		t.Fatalf("registered inside the default home: %v", err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(h.DefaultHome), ".claude.json")); err != nil {
		t.Fatal(err)
	}
	if p := problems(t, h, h.DefaultHome)["MCP server "+brand.Name]; p != "" {
		t.Fatalf("%q", p)
	}
}

func TestRemoveTakesOutTheThreeEntries(t *testing.T) {
	h, home := testHome(t, nil)
	original := `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"echo mine"}]}]},"permissions":{"allow":["Bash(ls *)"]}}`
	write(t, filepath.Join(home, "settings.json"), original)
	if _, err := h.Install(context.Background(), home, false); err != nil {
		t.Fatal(err)
	}
	if _, err := h.Remove(context.Background(), home, false); err != nil {
		t.Fatal(err)
	}
	var got, want any
	raw, _ := os.ReadFile(filepath.Join(home, "settings.json"))
	if err := errors.Join(json.Unmarshal(raw, &got), json.Unmarshal([]byte(original), &want)); err != nil {
		t.Fatal(err)
	}
	if a, b := fmt.Sprint(got), fmt.Sprint(want); a != b {
		t.Fatalf("remove left %s", raw)
	}
	if p := problems(t, h, home)["MCP server "+brand.Name]; !strings.Contains(p, "missing") {
		t.Fatalf("registration left: %q", p)
	}
	if lines, err := h.Remove(context.Background(), home, false); err != nil || len(lines) != 0 {
		t.Fatalf("second remove: %q %v", lines, err)
	}
}

func TestRemoveKeepsGoingWhenClaudeFails(t *testing.T) {
	h, home := testHome(t, nil)
	if _, err := h.Install(context.Background(), home, false); err != nil {
		t.Fatal(err)
	}
	h.Sh = func(context.Context, string, []string, string, string, ...string) ([]byte, error) {
		return []byte("boom"), errors.New("exit 1")
	}
	lines, err := h.Remove(context.Background(), home, false)
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("err = %v", err)
	}
	raw, _ := os.ReadFile(filepath.Join(home, "settings.json"))
	if strings.Contains(string(raw), brand.Name) || !slices.ContainsFunc(lines, func(l string) bool { return strings.HasPrefix(l, "removed hook Stop") }) {
		t.Fatalf("lines %q, settings %s", lines, raw)
	}
}

func TestTheClaudeHomeInstallsThePromptHookBesideOrcas(t *testing.T) {
	h, home := testHome(t, nil)
	const orcaHook = `curl -s localhost:$ORCA_AGENT_HOOK_PORT/hook`
	original := `{"hooks":{"UserPromptSubmit":[{"hooks":[{"type":"command","command":"` + orcaHook + `"}]}]}}`
	write(t, filepath.Join(home, "settings.json"), original)
	lines, err := h.Install(context.Background(), home, false)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(lines, func(l string) bool { return strings.HasPrefix(l, "added hook UserPromptSubmit") }) {
		t.Fatalf("lines %q", lines)
	}
	var commands []string
	raw, _ := os.ReadFile(filepath.Join(home, "settings.json"))
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	for _, hk := range hookCommands(doc, "UserPromptSubmit") {
		commands = append(commands, commandOf(hk))
	}
	if want := []string{orcaHook, "'/bin/" + brand.Name + "' hook prompt || true"}; !slices.Equal(commands, want) {
		t.Fatalf("UserPromptSubmit runs %q, want %q", commands, want)
	}
	if p := problems(t, h, home)["hook UserPromptSubmit"]; p != "" {
		t.Fatalf("check: %q", p)
	}
	if _, err := h.Remove(context.Background(), home, false); err != nil {
		t.Fatal(err)
	}
	raw, _ = os.ReadFile(filepath.Join(home, "settings.json"))
	var got, want any
	if err := errors.Join(json.Unmarshal(raw, &got), json.Unmarshal([]byte(original), &want)); err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("remove left %s", raw)
	}
}

func settingsList(t *testing.T, home, key string) []string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(home, "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	var s struct {
		Permissions map[string][]string `json:"permissions"`
	}
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatal(err)
	}
	return s.Permissions[key]
}

func TestInstallLetsSlackSendPostWithoutAsking(t *testing.T) {
	h, home := testHome(t, nil)
	write(t, filepath.Join(home, "settings.json"), `{"permissions":{"ask":["Bash(rm *)"]}}`)
	if _, err := h.Install(context.Background(), home, false); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(settingsList(t, home, "allow"), SlackSendTool) {
		t.Fatalf("allow = %q", settingsList(t, home, "allow"))
	}
	if got := settingsList(t, home, "ask"); !slices.Equal(got, []string{"Bash(rm *)"}) {
		t.Fatalf("ask = %q", got)
	}
	if p := problems(t, h, home)["permission "+SlackSendTool]; p != "" {
		t.Fatalf("check after install: %q", p)
	}
}

func TestInstallDropsTheAskRuleAnEarlierInstallAdded(t *testing.T) {
	h, home := testHome(t, nil)
	write(t, filepath.Join(home, "settings.json"), `{"permissions":{"ask":["Bash(rm *)","`+SlackSendTool+`"]}}`)
	if p := problems(t, h, home)["permission "+SlackSendTool]; p == "" {
		t.Fatal("check passes a home that asks before every Slack post")
	}
	lines, err := h.Install(context.Background(), home, false)
	if err != nil {
		t.Fatal(err)
	}
	if got := settingsList(t, home, "ask"); !slices.Equal(got, []string{"Bash(rm *)"}) {
		t.Fatalf("ask = %q", got)
	}
	if !slices.ContainsFunc(lines, func(l string) bool { return strings.Contains(l, "removed permission ask "+SlackSendTool) }) {
		t.Fatalf("lines %q", lines)
	}
	if p := problems(t, h, home)["permission "+SlackSendTool]; p != "" {
		t.Fatalf("check after install: %q", p)
	}
}

func TestUninstallRemovesTheSlackSendPermission(t *testing.T) {
	h, home := testHome(t, nil)
	write(t, filepath.Join(home, "settings.json"), `{"permissions":{"allow":["Bash(ls *)"],"ask":["Bash(rm *)"]}}`)
	if _, err := h.Install(context.Background(), home, false); err != nil {
		t.Fatal(err)
	}
	lines, err := h.Remove(context.Background(), home, false)
	if err != nil {
		t.Fatal(err)
	}
	if got := settingsList(t, home, "allow"); !slices.Equal(got, []string{"Bash(ls *)"}) {
		t.Fatalf("allow = %q", got)
	}
	if got := settingsList(t, home, "ask"); !slices.Equal(got, []string{"Bash(rm *)"}) {
		t.Fatalf("ask = %q", got)
	}
	if !slices.ContainsFunc(lines, func(l string) bool { return strings.Contains(l, "removed permission "+SlackSendTool) }) {
		t.Fatalf("lines %q", lines)
	}
}

func TestAStaticSessionHeaderIsFlaggedAndRewritten(t *testing.T) {
	h, home := testHome(t, nil)
	write(t, filepath.Join(home, ".claude.json"), `{"mcpServers":{"`+brand.Name+`":{"type":"http","url":"`+testURL+`","headers":{"X-Loop-Session":"${ORCA_TERMINAL_HANDLE}","X-Loop-Key":"`+testKey+`"}}}}`)
	if p := problems(t, h, home)["MCP server "+brand.Name]; !strings.Contains(p, "headersHelper") {
		t.Fatalf("a static session header: %q", p)
	}
	if _, err := h.Install(context.Background(), home, false); err != nil {
		t.Fatal(err)
	}
	if p := problems(t, h, home)["MCP server "+brand.Name]; p != "" {
		t.Fatalf("after install: %q", p)
	}
	raw, _ := os.ReadFile(filepath.Join(home, ".claude.json"))
	if strings.Contains(string(raw), "X-Loop-Session") || !strings.Contains(string(raw), "hook mcp-headers") {
		t.Fatalf("registration %s", raw)
	}
}

// linkedBinaries is a binary, a symlink to it, and another binary of the
// same name, as the shell words a Home runs them by.
func linkedBinaries(t *testing.T) (direct, linked, other string) {
	t.Helper()
	root := t.TempDir()
	target := filepath.Join(root, "checkout", "bin", brand.Command)
	link := filepath.Join(root, "local bin", brand.Command)
	elsewhere := filepath.Join(root, "elsewhere", brand.Command)
	write(t, target, "#!/bin/sh\n")
	write(t, elsewhere, "#!/bin/sh\n")
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	return shell.Quote(target), shell.Quote(link), shell.Quote(elsewhere)
}

func TestABinaryThroughASymlinkJudgesTheHooksOfItsTargetRight(t *testing.T) {
	direct, linked, _ := linkedBinaries(t)
	for _, c := range []struct{ installed, checking string }{{direct, linked}, {linked, direct}} {
		h, home := testHome(t, nil)
		h.Command = c.installed
		if _, err := h.Install(context.Background(), home, false); err != nil {
			t.Fatal(err)
		}
		h.Command = c.checking
		for entry, p := range problems(t, h, home) {
			if p != "" {
				t.Errorf("installed by %s, checked by %s: %s: %s", c.installed, c.checking, entry, p)
			}
		}
	}
}

func TestInstallWritesThePathItRunsByOverTheSameBinarysOther(t *testing.T) {
	direct, linked, _ := linkedBinaries(t)
	h, home := testHome(t, nil)
	h.Command = direct
	if _, err := h.Install(context.Background(), home, false); err != nil {
		t.Fatal(err)
	}
	h.Command = linked
	lines, err := h.Install(context.Background(), home, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"changed MCP server", "changed hook Stop", "changed hook SessionStart", "changed hook UserPromptSubmit"} {
		if !slices.ContainsFunc(lines, func(l string) bool { return strings.HasPrefix(l, want) }) {
			t.Errorf("no %q in %q", want, lines)
		}
	}
	for _, f := range h.Files(home) {
		raw, _ := os.ReadFile(f)
		if strings.Contains(string(raw), strings.Trim(direct, "'")) {
			t.Errorf("%s still runs %s:\n%s", f, direct, raw)
		}
	}
}

func TestTheSameBinaryRunningAnotherCommandIsWrong(t *testing.T) {
	direct, linked, _ := linkedBinaries(t)
	h, home := testHome(t, nil)
	h.Command = direct
	write(t, filepath.Join(home, "settings.json"), `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"`+linked+` hook stop --agent x || true","timeout":10}]}]}}`)
	if p := problems(t, h, home)["hook Stop"]; !strings.Contains(p, "--agent x") {
		t.Errorf("stop: %q", p)
	}
}

func TestARelativePathToTheSameBinaryIsWrong(t *testing.T) {
	direct, _, _ := linkedBinaries(t)
	bin := strings.Trim(direct, "'")
	t.Chdir(filepath.Dir(bin))
	h, home := testHome(t, nil)
	h.Command = direct
	write(t, filepath.Join(home, "settings.json"), `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"./`+brand.Command+` hook stop || true","timeout":10}]}]}}`)
	if p := problems(t, h, home)["hook Stop"]; !strings.Contains(p, "./"+brand.Command) {
		t.Errorf("stop: %q", p)
	}
}

func TestAnotherBinaryOfTheSameNameIsStillWrong(t *testing.T) {
	direct, _, other := linkedBinaries(t)
	h, home := testHome(t, nil)
	h.Command = other
	if _, err := h.Install(context.Background(), home, false); err != nil {
		t.Fatal(err)
	}
	h.Command = direct
	got := problems(t, h, home)
	for _, entry := range []string{"MCP server " + brand.MCPServer(), "hook Stop", "hook SessionStart", "hook UserPromptSubmit"} {
		if !strings.Contains(got[entry], other) {
			t.Errorf("%s: %q", entry, got[entry])
		}
	}
}

func TestALinkOfAnotherNameRunsTheSameHooks(t *testing.T) {
	direct, _, _ := linkedBinaries(t)
	link := filepath.Join(t.TempDir(), "wl")
	if err := os.Symlink(strings.Trim(direct, "'"), link); err != nil {
		t.Fatal(err)
	}
	h, home := testHome(t, nil)
	h.Command = shell.Quote(link)
	if _, err := h.Install(context.Background(), home, false); err != nil {
		t.Fatal(err)
	}
	h.Command = direct
	for entry, p := range problems(t, h, home) {
		if p != "" {
			t.Errorf("%s: %s", entry, p)
		}
	}
	if _, err := h.Install(context.Background(), home, false); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(filepath.Join(home, "settings.json"))
	if strings.Count(string(raw), "hook stop") != 1 || strings.Contains(string(raw), link) {
		t.Fatalf("install over the link:\n%s", raw)
	}
	h.Command = shell.Quote(link)
	if _, err := h.Remove(context.Background(), home, false); err != nil {
		t.Fatal(err)
	}
	if raw, _ := os.ReadFile(filepath.Join(home, "settings.json")); strings.Contains(string(raw), "hook ") {
		t.Fatalf("remove by the link left:\n%s", raw)
	}
}
