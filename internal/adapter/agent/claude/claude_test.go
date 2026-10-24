package claude

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/core/turns"
)

var (
	_ turns.Agent         = Agent{}
	_ turns.HeadlessAgent = Agent{}
)

func TestClaudeCommand(t *testing.T) {
	a := Agent{}
	for _, c := range []struct {
		s    turns.AgentSession
		want string
	}{
		{turns.AgentSession{}, "claude"},
		{turns.AgentSession{Home: "/home/me/My Claude"}, "CLAUDE_CONFIG_DIR='/home/me/My Claude' claude"},
		{turns.AgentSession{Worktree: "/w/x y", Policy: turns.Policy{Deny: [][]string{{"git", "push"}}, NoMCP: true}},
			`claude --settings '{"permissions":{"allow":[],"deny":["Bash(git push *)"]}}' --strict-mcp-config`},
	} {
		got, err := a.Command(c.s)
		if err != nil || got != c.want {
			t.Errorf("Command(%+v) = %q, %v; want %q", c.s, got, err, c.want)
		}
	}
}

func TestClaudeSettings(t *testing.T) {
	var got struct {
		Permissions struct{ Allow, Deny []string } `json:"permissions"`
	}
	raw := Agent{}.Settings(turns.Policy{Allow: [][]string{{"gh", "api"}}, Deny: [][]string{{"git", "push"}}})
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got.Permissions.Allow, []string{"Bash(gh api *)"}) || !slices.Equal(got.Permissions.Deny, []string{"Bash(git push *)"}) {
		t.Fatalf("settings %s", raw)
	}
}

func TestClaudeHeadlessArgs(t *testing.T) {
	var gotEnv, gotArgs []string
	var gotStdin string
	sh := func(_ context.Context, dir string, env []string, stdin, bin string, args ...string) ([]byte, error) {
		gotEnv, gotArgs, gotStdin = env, append([]string{bin}, args...), stdin
		return []byte("ok"), nil
	}
	wt := t.TempDir()
	out, err := Agent{Sh: sh}.Headless(context.Background(),
		turns.AgentSession{Worktree: wt, Home: "/h", Policy: turns.Policy{Deny: [][]string{{"git", "push"}}, NoMCP: true}}, "do it")
	if err != nil || string(out) != "ok" {
		t.Fatalf("%q %v", out, err)
	}
	args := strings.Join(gotArgs, " ")
	for _, want := range []string{"claude -p", "--no-session-persistence", `--settings {"permissions"`, "--strict-mcp-config"} {
		if !strings.Contains(args, want) {
			t.Errorf("args %q lack %q", args, want)
		}
	}
	if !slices.Contains(gotEnv, "CLAUDE_CONFIG_DIR=/h") || gotStdin != "do it" {
		t.Fatalf("env %q stdin %q", gotEnv, gotStdin)
	}
}

func TestClaudeStrictPolicyPinsThePermissionModeAndTheSettingSources(t *testing.T) {
	got, _ := Agent{}.Command(turns.AgentSession{Worktree: "/w", Policy: turns.Policy{Strict: true, Allow: [][]string{{"git", "diff"}}}})
	if !strings.Contains(got, "--permission-mode default") || !strings.Contains(got, "--setting-sources user") {
		t.Fatalf("%s", got)
	}
	got, _ = Agent{}.Command(turns.AgentSession{Worktree: "/w", Policy: turns.Policy{Allow: [][]string{{brand.Name, "instructions"}}}})
	if strings.Contains(got, "--permission-mode") {
		t.Fatalf("a round session lost the user's permission mode: %s", got)
	}
}

func TestUserSettingsOnlyKeepsThePermissionModeAndDropsTheProjectSources(t *testing.T) {
	got, _ := Agent{}.Command(turns.AgentSession{Worktree: "/w", Policy: turns.Policy{UserSettingsOnly: true, TurnEnd: true}})
	if !strings.Contains(got, "--setting-sources user") || strings.Contains(got, "--permission-mode") || strings.Contains(got, "--strict-mcp-config") {
		t.Fatalf("%s", got)
	}
	if strings.Contains(got, "disableAllHooks") {
		t.Fatalf("the user's own hooks went off: %s", got)
	}
	got, _ = Agent{}.Command(turns.AgentSession{Worktree: "/w", Policy: turns.Policy{UserSettingsOnly: true}})
	if !strings.Contains(got, "--setting-sources user") {
		t.Fatalf("a policy with only UserSettingsOnly read as empty: %s", got)
	}
	got, _ = Agent{}.Command(turns.AgentSession{Worktree: "/w", Policy: turns.Policy{UserSettingsOnly: true, Strict: true}})
	if strings.Count(got, "--setting-sources") != 1 {
		t.Fatalf("%s", got)
	}
}

func TestClaudeRulesKeepATrailingWildcardToken(t *testing.T) {
	raw := Agent{}.Settings(turns.Policy{Deny: [][]string{{"gh", "api", "--method=*"}}, WritePaths: []string{".loop"}})
	for _, want := range []string{"Bash(gh api --method=*)", "Edit(./.loop/**)"} {
		if !strings.Contains(raw, want) {
			t.Errorf("lacks %s:\n%s", want, raw)
		}
	}
	if strings.Contains(raw, "Write(") {
		t.Errorf("Claude Code warns that Write rules match nothing; Edit covers every file tool:\n%s", raw)
	}
}

func TestAStrictPolicyTurnsOffTheUsersHooks(t *testing.T) {
	if !strings.Contains(Agent{}.Settings(turns.Policy{Strict: true}), `"disableAllHooks":true`) {
		t.Fatal("a PreToolUse hook that rewrites a command would take it out of the policy's allow rules")
	}
	if strings.Contains(Agent{}.Settings(turns.Policy{Allow: [][]string{{brand.Name, "instructions"}}}), "disableAllHooks") {
		t.Fatal("a round session lost the user's hooks")
	}
}

func TestCommandAddsTheModel(t *testing.T) {
	got, err := Agent{}.Command(turns.AgentSession{Model: "opus", Home: "/h"})
	if err != nil || got != "CLAUDE_CONFIG_DIR='/h' claude --model 'opus'" {
		t.Fatalf("%q %v", got, err)
	}
	if got, _ := (Agent{}).Command(turns.AgentSession{}); strings.Contains(got, "--model") {
		t.Fatalf("a session without a model named one: %s", got)
	}
}

func TestHeadlessPassesTheSessionModel(t *testing.T) {
	var gotArgs []string
	sh := func(_ context.Context, _ string, _ []string, _, _ string, args ...string) ([]byte, error) {
		gotArgs = args
		return nil, nil
	}
	if _, err := (Agent{Sh: sh}).Headless(context.Background(), turns.AgentSession{Model: "haiku"}, ""); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(gotArgs, " "), "--model haiku") {
		t.Fatalf("args %q", gotArgs)
	}
}

func TestTurnEndAloneIsNotAnEmptyPolicy(t *testing.T) {
	p := turns.Policy{TurnEnd: true}
	if raw := (Agent{}).Settings(p); !strings.Contains(raw, `"allow":["`+brand.ToolPrefix()+`turn_end"]`) {
		t.Fatalf("%s", raw)
	}
	got, _ := (Agent{}).Command(turns.AgentSession{Policy: p})
	if !strings.Contains(got, "--settings") || !strings.Contains(got, brand.ToolPrefix()+"turn_end") {
		t.Fatalf("a policy with only the turn end dropped its settings: %s", got)
	}
}

func TestResumeMatchesTheCleanupLine(t *testing.T) {
	for _, c := range []struct{ home, id, want string }{
		{"/home/me/.claude-p", "s-1", "CLAUDE_CONFIG_DIR='/home/me/.claude-p' claude --resume s-1"},
		{"", "", "claude --resume"},
		{"/it's", "", `CLAUDE_CONFIG_DIR='/it'\''s' claude --resume`},
	} {
		if got := (Agent{}).Resume(c.home, c.id); got != c.want {
			t.Errorf("Resume(%q, %q) = %q; want %q", c.home, c.id, got, c.want)
		}
	}
}

func TestBinaryIsClaude(t *testing.T) {
	if got := (Agent{}).Binary(); got != "claude" {
		t.Fatal(got)
	}
}

func TestSettingsOfTheRoundPolicyArePinned(t *testing.T) {
	p := turns.Policy{Allow: [][]string{{"/bin/" + brand.Name, "turn", "end"}}, WritePaths: []string{".loop"}, TurnEnd: true}
	want := `{"permissions":{"allow":["Bash(/bin/` + brand.Name + ` turn end *)","` + brand.ToolPrefix() + `turn_end","Edit(./.loop/**)"],"deny":[]}}`
	if got := (Agent{}).Settings(p); got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
}

func TestHeadlessRunsInTheWorktreeWithTheHome(t *testing.T) {
	var gotDir, gotStdin, gotBin string
	var gotEnv, gotArgs []string
	sh := func(_ context.Context, dir string, env []string, stdin, bin string, args ...string) ([]byte, error) {
		gotDir, gotEnv, gotStdin, gotBin, gotArgs = dir, env, stdin, bin, args
		return nil, nil
	}
	s := turns.AgentSession{Worktree: "/wt", Home: "/h", Policy: turns.Policy{Strict: true, NoMCP: true}}
	if _, err := (Agent{Sh: sh}).Headless(context.Background(), s, "do it"); err != nil {
		t.Fatal(err)
	}
	if gotDir != "/wt" || !slices.Equal(gotEnv, []string{"CLAUDE_CONFIG_DIR=/h"}) || gotStdin != "do it" || gotBin != "claude" {
		t.Fatalf("dir %q env %q stdin %q bin %q", gotDir, gotEnv, gotStdin, gotBin)
	}
	if len(gotArgs) < 3 || !slices.Equal(gotArgs[:3], []string{"-p", "--no-session-persistence", "--settings"}) {
		t.Fatalf("args %q", gotArgs)
	}
}

func TestDraftToolAllowedSendStillAsks(t *testing.T) {
	allowed := func(p turns.Policy) []string {
		var got struct {
			Permissions struct{ Allow []string } `json:"permissions"`
		}
		if err := json.Unmarshal([]byte(Agent{}.Settings(p)), &got); err != nil {
			t.Fatal(err)
		}
		return got.Permissions.Allow
	}
	with := allowed(turns.Policy{TurnEnd: true, Drafts: true})
	if !slices.Contains(with, SlackDraftTool) || SlackDraftTool != brand.ToolPrefix()+"slack_draft" {
		t.Fatalf("allow %v lacks %s", with, SlackDraftTool)
	}
	for _, tool := range []string{SlackSendTool, brand.ToolPrefix() + "slack_read"} {
		if slices.Contains(with, tool) {
			t.Fatalf("allow %v holds %s", with, tool)
		}
	}
	if slices.Contains(allowed(turns.Policy{TurnEnd: true}), SlackDraftTool) {
		t.Fatal("a policy without Drafts allows slack_draft")
	}
	if emptyPolicy(turns.Policy{Drafts: true}) {
		t.Fatal("a Drafts-only policy passes no settings")
	}
	doc := map[string]any{}
	Home{}.installSettings(doc)
	perms, _ := doc["permissions"].(map[string]any)
	if ask, _ := perms["ask"].([]any); !slices.Contains(ask, any(SlackSendTool)) {
		t.Fatalf("installed ask %v", perms["ask"])
	}
	if allow, _ := perms["allow"].([]any); slices.Contains(allow, any(SlackDraftTool)) || slices.Contains(allow, any(SlackSendTool)) {
		t.Fatalf("installed allow %v", perms["allow"])
	}
}
