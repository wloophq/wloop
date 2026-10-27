// Package claude is the Claude Code agent.
package claude

import (
	"context"
	"encoding/json"
	"slices"
	"strings"

	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/core/turns"
	"github.com/wloophq/wloop/internal/domain/shell"
)

// ShellFunc runs bin in dir with env added to the process environment; an
// entry without "=" removes that variable.
type ShellFunc func(ctx context.Context, dir string, env []string, stdin, bin string, args ...string) ([]byte, error)

type Agent struct {
	Sh ShellFunc
	// AskSh runs the calls of the Kind's Asker, which read stdout alone.
	AskSh ShellFunc
	// UserHome names the default home.
	UserHome string
}

func (k Agent) defaultHome() string {
	if k.UserHome == "" {
		return ""
	}
	return DefaultHome(k.UserHome)
}

// usesDefault tells a session of the default home, which Claude Code reads
// without CLAUDE_CONFIG_DIR; an Orca terminal sets it to its profile home.
func (k Agent) usesDefault(home string) bool { return isDefault(home, k.defaultHome()) }

// SlackDraftTools only store drafts the user approves, so a session policy may allow them.
var SlackDraftTools = []string{brand.ToolPrefix() + "slack_draft"}

func (Agent) Name() string { return "claude" }

func (Agent) Binary() string { return "claude" }

func (Agent) Resume(home, agentSession string) string {
	var parts []string
	if home != "" {
		parts = append(parts, "CLAUDE_CONFIG_DIR="+shell.Quote(home))
	}
	parts = append(parts, "claude --resume")
	if agentSession != "" {
		parts = append(parts, agentSession)
	}
	return strings.Join(parts, " ")
}

func emptyPolicy(p turns.Policy) bool {
	return len(p.Allow) == 0 && len(p.Deny) == 0 && len(p.WritePaths) == 0 && !p.TurnEnd && !p.Drafts && !p.NoGitHubWrites && !p.NoMCP && !p.Strict && !p.UserSettingsOnly && len(p.Dirs) == 0 && len(p.DenyPaths) == 0
}

// IsDefaultHome says whether home is the default home, "" included.
func (k Agent) IsDefaultHome(home string) bool { return home == "" || k.usesDefault(home) }

func (k Agent) flags(s turns.AgentSession) []string {
	var out []string
	switch {
	case s.Resume != "":
		out = append(out, "--resume", s.Resume)
	case s.SessionID != "":
		out = append(out, "--session-id", s.SessionID)
	}
	if s.Model != "" {
		out = append(out, "--model", s.Model)
	}
	if s.Name != "" {
		out = append(out, "--name", s.Name)
	}
	if emptyPolicy(s.Policy) {
		return out
	}
	out = append(out, "--settings", k.Settings(s.Policy))
	switch {
	case s.Policy.Strict:
		out = append(out, "--permission-mode", "default", "--setting-sources", "user")
	case s.Policy.UserSettingsOnly:
		out = append(out, "--setting-sources", "user")
	}
	if s.Policy.NoMCP {
		out = append(out, "--strict-mcp-config")
	}
	for _, d := range s.Policy.Dirs {
		out = append(out, "--add-dir", d.Path)
	}
	return out
}

func (k Agent) Command(s turns.AgentSession) (string, error) {
	parts := []string{"claude"}
	switch {
	case s.Home == "":
	case k.usesDefault(s.Home):
		parts = append([]string{"env", "-u", "CLAUDE_CONFIG_DIR"}, parts...)
	default:
		parts = append([]string{"CLAUDE_CONFIG_DIR=" + shell.Quote(s.Home)}, parts...)
	}
	flags := k.flags(s)
	for i, f := range flags {
		if i > 0 && slices.Contains([]string{"--settings", "--model", "--name", "--add-dir", "--resume", "--session-id"}, flags[i-1]) {
			f = shell.Quote(f)
		}
		parts = append(parts, f)
	}
	return strings.Join(parts, " "), nil
}

func rules(prefixes [][]string) []string {
	out := []string{}
	for _, p := range prefixes {
		rule := strings.Join(p, " ")
		if !strings.HasSuffix(p[len(p)-1], "*") {
			rule += " *"
		}
		out = append(out, "Bash("+rule+")")
	}
	return out
}

// Settings renders a policy as the JSON --settings takes.
func (Agent) Settings(p turns.Policy) string {
	var settings struct {
		Permissions struct {
			Allow []string `json:"allow"`
			Deny  []string `json:"deny"`
			// AdditionalDirectories carries the --add-dir grants for a manager
			// that passes no flags, as Claude Desktop.
			AdditionalDirectories []string `json:"additionalDirectories,omitempty"`
		} `json:"permissions"`
		DisableAllHooks bool `json:"disableAllHooks,omitempty"`
	}
	settings.DisableAllHooks = p.Strict
	settings.Permissions.Allow = rules(p.Allow)
	if p.TurnEnd {
		settings.Permissions.Allow = append(settings.Permissions.Allow, TurnEndTool)
	}
	if p.Drafts {
		settings.Permissions.Allow = append(settings.Permissions.Allow, SlackDraftTools...)
	}
	for _, d := range p.WritePaths {
		settings.Permissions.Allow = append(settings.Permissions.Allow, "Edit(./"+d+"/**)")
	}
	for _, d := range p.Dirs {
		for _, w := range d.Write {
			// An absolute path takes Claude Code's //abs rule form.
			settings.Permissions.Allow = append(settings.Permissions.Allow, "Edit(/"+d.Path+"/"+w+"/**)")
		}
	}
	settings.Permissions.Deny = rules(p.Deny)
	for _, d := range p.Dirs {
		settings.Permissions.AdditionalDirectories = append(settings.Permissions.AdditionalDirectories, d.Path)
	}
	for _, d := range p.Dirs {
		settings.Permissions.Deny = append(settings.Permissions.Deny, "Edit(/"+d.Path+"/.git/**)")
	}
	for _, d := range p.DenyPaths {
		// An absolute path takes Claude Code's //abs rule form.
		settings.Permissions.Deny = append(settings.Permissions.Deny, "Read(/"+d+"/**)", "Edit(/"+d+"/**)")
	}
	raw, _ := json.Marshal(settings)
	return string(raw)
}

// homeEnv is the environment that points claude at home, in ShellFunc form.
func homeEnv(home, defaultHome string) []string {
	switch {
	case home == "":
		return nil
	case isDefault(home, defaultHome):
		return []string{"CLAUDE_CONFIG_DIR"}
	}
	return []string{"CLAUDE_CONFIG_DIR=" + home}
}

func (k Agent) Headless(ctx context.Context, s turns.AgentSession, prompt string) ([]byte, error) {
	args := append([]string{"-p", "--no-session-persistence"}, k.flags(s)...)
	return k.Sh(ctx, s.Worktree, homeEnv(s.Home, k.defaultHome()), prompt, "claude", args...)
}
