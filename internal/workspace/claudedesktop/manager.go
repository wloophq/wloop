// Package claudedesktop implements workspace.Manager over the Claude Desktop
// app: git worktrees under a root the loop owns, and the Claude Code sessions
// Desktop runs in them.
package claudedesktop

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/workspace"
)

const (
	// Name is the manager's key in the workspace manager registry.
	Name = "claude-desktop"
	// Agent is the only agent Desktop runs.
	Agent = "claude"

	entrypoint = "claude-desktop"
	appPath    = "/Applications/Claude.app"
	readyWait  = 20 * time.Second
)

// Control is the argv prefixes that drive Desktop, which a session must not run.
var Control = [][]string{{"open", "claude://*"}}

// ControlPaths are the paths through which a process can address Desktop's
// sessions: the process registry with its peer tokens, and their sockets.
// AutoFix is doctor's advice on Desktop's PR Auto-fix, which Desktop keeps
// nowhere on disk the loop can read.
var AutoFix = struct{ Name, Info, Fix string }{
	Name: "claude desktop pr auto-fix",
	Info: "Claude Desktop does not store this setting where " + brand.Name + " can read it; when it is on, Desktop starts turns of its own on your pull requests that compete with " + brand.Name + "'s",
	Fix:  "turn PR Auto-fix off in Claude Desktop's settings",
}

func ControlPaths(userHome string) []string {
	return []string{filepath.Join(userHome, ".claude", "sessions"), "/tmp/cc-socks"}
}

type Manager struct {
	// Root holds the worktrees, as <Root>/<repo name>/<name>, and the manager's own state.
	Root string
	// SessionsDir is Claude Code's process registry.
	SessionsDir string
	// StoreDir is Desktop's session store, <StoreDir>/<account>/<org>/local_<uuid>.json.
	StoreDir string
	// AppPath is the Desktop app bundle.
	AppPath string
	Exec    Runner
	// Environ is the environment a mint starts from.
	Environ func() []string
	// Alive reports whether a registry entry's process still runs.
	Alive func(pid int) bool
	// Kill ends a session's process; nil sends it SIGTERM.
	Kill func(pid int) error
	// NewUUID mints the uuid of a woken session's fork; nil mints random ones.
	NewUUID func() string
	// MCP is the loop's MCP server name and its registration for the process
	// a handle addresses; nil registers none.
	MCP   func(handle string) (name string, entry map[string]any)
	Now   func() time.Time
	Sleep func(ctx context.Context, d time.Duration) error
	// ReadyWait bounds the wait for a session's process to join the registry.
	ReadyWait time.Duration

	mu sync.Mutex
}

var (
	_ workspace.Manager        = (*Manager)(nil)
	_ workspace.Observer       = (*Manager)(nil)
	_ workspace.Waker          = (*Manager)(nil)
	_ workspace.PromptRewriter = (*Manager)(nil)
	_ workspace.PolicyHonorer  = (*Manager)(nil)
	_ workspace.TerminalCloser = (*Manager)(nil)
)

// New is a manager of the worktrees under root, reading the registry and
// store of the user whose home is userHome.
func New(root, userHome string) *Manager {
	return &Manager{
		Root:        root,
		SessionsDir: filepath.Join(userHome, ".claude", "sessions"),
		StoreDir:    filepath.Join(userHome, "Library", "Application Support", "Claude", "claude-code-sessions"),
		AppPath:     appPath,
		Exec:        Metered{Next: Exec{}},
		Environ:     os.Environ,
		Alive:       processAlive,
		Now:         time.Now,
		Sleep:       sleep,
		ReadyWait:   readyWait,
	}
}

func processAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

func sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func (m *Manager) Name() string { return Name }

func (m *Manager) Hosts(agent string) bool { return agent == Agent }

// Available is nil when Desktop is installed and the claude CLI that mints
// sessions is on PATH.
func (m *Manager) Available(ctx context.Context) error {
	if _, err := os.Stat(m.AppPath); err != nil {
		return fmt.Errorf("the Claude Desktop app is not installed at %s", m.AppPath)
	}
	if _, err := exec.LookPath("claude"); err != nil {
		return errors.New("the claude CLI is not on PATH")
	}
	return nil
}

// Reachable is nil while the Desktop app runs.
func (m *Manager) Reachable(ctx context.Context) error {
	_, err := m.runtime(ctx)
	return err
}

// ConnectsMCPOnFirstPrompt is true: Desktop runs Claude Code in SDK mode,
// which connects the MCP servers when the session's first query starts.
func (m *Manager) ConnectsMCPOnFirstPrompt() bool { return true }

// AppRunning is Reachable: the worktrees read from disk stay listable while
// the app is down, so the cache must ask the app itself.
func (m *Manager) AppRunning(ctx context.Context) error { return m.Reachable(ctx) }

// runtime names the running Desktop app by its pid and start time, so a
// restart reads as another runtime.
func (m *Manager) runtime(ctx context.Context) (string, error) {
	out, err := m.Exec.Run(ctx, Cmd{Name: "ps", Args: []string{"-axo", "pid=,lstart=,comm="}})
	if err != nil {
		return "", fmt.Errorf("%w: listing processes: %w", workspace.ErrUnreachable, err)
	}
	bin := filepath.Join(m.AppPath, "Contents", "MacOS", "Claude")
	for line := range strings.SplitSeq(string(out), "\n") {
		f := strings.Fields(line)
		// pid, the five words of lstart, then the command path, which may hold spaces.
		if len(f) < 7 || strings.Join(f[6:], " ") != bin {
			continue
		}
		return f[0] + "@" + strings.Join(f[1:6], " "), nil
	}
	return "", fmt.Errorf("%w: the Claude Desktop app is not running", workspace.ErrUnreachable)
}

// Honors is nil for a launch that loads the worktree's settings in the
// default home with every MCP server: the only launch Desktop makes.
func (m *Manager) Honors(needs workspace.LaunchNeeds) error {
	var why []string
	if needs.UserSettingsOnly {
		why = append(why, "it loads the worktree's own settings")
	}
	if needs.Strict {
		why = append(why, "it cannot make the policy the session's only permissions")
	}
	if needs.NoMCP {
		why = append(why, "it loads every configured MCP server")
	}
	if needs.CustomHome {
		why = append(why, "it runs only the default Claude home")
	}
	if len(why) == 0 {
		return nil
	}
	return fmt.Errorf("the Claude Desktop app cannot honor the launch policy: %s", strings.Join(why, "; "))
}

// RewritePrompt words a slash command as an instruction: Desktop delivers a
// prompt as a peer message, which Claude Code does not expand.
func (m *Manager) RewritePrompt(prompt string) string {
	rest, ok := strings.CutPrefix(prompt, "/")
	skill, args, _ := strings.Cut(rest, " ")
	if !ok || skill == "" || strings.ContainsAny(skill, "\n\t/") {
		return prompt
	}
	args = strings.Join(strings.Fields(args), " ")
	if args == "" {
		return "Invoke the skill " + skill + "."
	}
	return "Invoke the skill " + skill + " with the arguments: " + args
}

func (m *Manager) now() time.Time {
	if m.Now == nil {
		return time.Now()
	}
	return m.Now()
}
