// Package turns holds the core of a turn and the ports it drives.
package turns

import (
	"context"
	"slices"
	"strings"

	"github.com/wloophq/wloop/internal/brand"
)

// Policy is what a session may run, as command prefixes; each agent
// translates it into its own permission settings.
type Policy struct {
	// Allow and Deny are command prefixes; a token may hold a * wildcard.
	Allow, Deny [][]string
	// WritePaths are directories, relative to the worktree, the agent may
	// write files in.
	WritePaths []string
	// TurnEnd lets the session call the loop's turn_end tool without a prompt.
	TurnEnd bool
	// Drafts lets the session call the loop's slack_draft tool without a prompt; it posts nothing.
	Drafts         bool
	NoGitHubWrites bool
	NoMCP          bool
	// Strict makes the policy the only permissions: the user's permission
	// mode, hooks and the worktree's own settings do not apply. A hook that
	// rewrites a command takes it out of the allow rules.
	Strict bool
	// UserSettingsOnly starts the session with the user's own agent
	// settings alone: the worktree's settings, hooks, MCP servers and
	// instruction files do not load.
	UserSettingsOnly bool
	// Dirs are directories outside the worktree the agent may read, and write under their Write subpaths.
	Dirs []Dir
	// DenyPaths are absolute paths the agent may neither read nor write.
	DenyPaths []string
}

// Control is what drives the workspace managers: command prefixes and paths a
// session the loop starts may neither run nor read, so it cannot steer the
// manager that hosts it or another session.
type Control struct {
	Commands [][]string
	Paths    []string
}

// Denied is p with every command and path of c denied.
func (p Policy) Denied(c Control) Policy {
	p.Deny = append(slices.Clone(p.Deny), c.Commands...)
	p.DenyPaths = append(slices.Clone(p.DenyPaths), c.Paths...)
	return p
}

// Dir is an absolute path and the subpaths the agent may write, e.g. log, sources, drafts.
type Dir struct {
	Path  string
	Write []string
}

type AgentSession struct {
	// SessionID is the id the loop minted for the session, which the agent
	// takes as its own conversation id; "" lets the agent pick one.
	SessionID string
	// Resume is the agent's own id of a conversation the session resumes,
	// "" for a new one.
	Resume   string
	Worktree string
	// Home is the agent's configuration directory, "" for its default.
	Home string
	// Model is a model name of the agent's own that the user pinned, "" for
	// its default; the core passes it on unread.
	Model string
	// Name is what the agent shows the session as, "" for its default.
	Name   string
	Policy Policy
	// Trusted says the pull request passes the trust check of automated
	// rounds, so the session may run unattended. The loop never trusts the
	// worktree in the agent's home for it: that stays the user's decision.
	Trusted bool
	// TrustedAtLaunch says the workspace manager may trust the worktree in
	// Home as it launches the session, after any preflight reads it.
	TrustedAtLaunch bool
}

// SettingsRenderer is an agent that renders a policy as the permission
// settings it reads, for a manager that writes them where the agent loads
// them instead of passing them on the command line.
type SettingsRenderer interface {
	Settings(p Policy) string
}

// HomeDefaulter is an agent that tells its default configuration home from
// another one.
type HomeDefaulter interface {
	IsDefaultHome(home string) bool
}

// Agent starts interactive sessions of one coding agent.
type Agent interface {
	Name() string
	// Binary is the executable an interactive session runs.
	Binary() string
	// Command is the shell line that starts an interactive session.
	Command(s AgentSession) (string, error)
	// Preflight checks, without changing anything, that a session of s can
	// start unattended; an *UntrustedError names a worktree the agent's
	// home does not trust.
	Preflight(ctx context.Context, s AgentSession) error
	// Resume is the shell line that resumes agentSession with home, "" for
	// the agent's default home.
	Resume(home, agentSession string) string
}

// HeadlessAgent is an Agent that also runs one session to its end
// without a terminal, in s.Worktree with s.Home.
type HeadlessAgent interface {
	Headless(ctx context.Context, s AgentSession, prompt string) ([]byte, error)
}

// QuestionTrust is the agent's question whether to trust the worktree.
const QuestionTrust = "trust"

// Questions is what an agent knows of the questions a starting session
// stops on that only the user answers. None of it writes.
type Questions interface {
	// BlockedOn names the question a session's screen shows that the
	// workspace manager does not report, "" for none.
	BlockedOn(screen string) string
	// Explain is the error that tells the user what to do about question in
	// a session of s, nil for a question the kind does not know. A trust
	// question is an *UntrustedError, any other a *QuestionError.
	Explain(ctx context.Context, s AgentSession, question string) error
	// Unanswered says whether s's home still records no answer to question
	// for s's worktree.
	Unanswered(ctx context.Context, s AgentSession, question string) bool
}

// UntrustedError is a worktree the agent's home does not trust, so its
// session waits, or would wait, on the agent's trust dialog.
type UntrustedError struct {
	Folder string
	// Home is "" when the session runs with its terminal's own home.
	Home string
	// Root is the main checkout whose trust covers every worktree of its
	// repository; it outlives a worktree the loop removes.
	Root string
	// Fix is the command that trusts Root in Home, "" when the loop does not
	// suggest one.
	Fix string
	// Terminal is the terminal waiting on the dialog, "" for a preflight.
	Terminal string
	Cause    error
}

func (e *UntrustedError) Error() string {
	var b strings.Builder
	b.WriteString("untrusted folder blocks the agent: " + e.Folder + " is not trusted in ")
	if e.Home == "" {
		b.WriteString("the agent home of its terminal (set agent_home to have " + brand.Name + " name it)")
	} else {
		b.WriteString("the agent home " + e.Home)
	}
	if e.Terminal != "" {
		b.WriteString(", so its terminal " + e.Terminal + " waits on the trust dialog")
	}
	switch {
	case e.Fix == "":
	case e.Root == "" || e.Root == e.Folder:
		b.WriteString("; to trust it, run: " + e.Fix)
	default:
		b.WriteString("; to trust " + e.Root + " and its worktrees, run: " + e.Fix)
	}
	return b.String()
}

func (e *UntrustedError) Unwrap() error { return e.Cause }

// QuestionError is a session start that waits on a question of the agent
// only the user answers.
type QuestionError struct {
	Question string
	// Root is the folder whose answer the agent's home records.
	Root    string
	Message string
	Cause   error
}

func (e *QuestionError) Error() string { return e.Message }

func (e *QuestionError) Unwrap() error { return e.Cause }
