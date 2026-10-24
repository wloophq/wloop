// Package workspace is the port between the review loop and whatever tool
// manages worktrees and agent sessions. Orca is the only adapter today.
package workspace

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/unit"
	wsdomain "github.com/wloophq/wloop/internal/domain/workspace"
)

var ErrUnknownStatus = errors.New("unknown workspace status")

// ErrUnreachable is a manager that cannot be asked now: not running, socket refused, or
// its runtime restarted mid-call. A read that fails with it says nothing about the data.
var ErrUnreachable = errors.New("workspace manager not reachable")

type CreateRequest struct {
	RepoID  string
	Name    string
	BaseRef string
	// Unit is the unit the workspace is created for; a pull request is linked as the manager links one.
	Unit   unit.Unit
	Agent  string
	Prompt string
	// HeadBranch, HeadOid, BaseBranchName and CrossRepository describe the
	// linked PR; with them the workspace's branch is named after the PR's head
	// branch.
	HeadBranch      string
	HeadOid         string
	BaseBranchName  string
	CrossRepository bool
}

// LinkedPR is the pull request number of a github unit, 0 for any other.
func LinkedPR(u unit.Unit) int {
	if u.Provider != unit.GitHub {
		return 0
	}
	ref, err := pr.ParseKey(u.Key)
	if err != nil {
		return 0
	}
	return ref.Number
}

type Session struct {
	Handle     string
	LastOutput time.Time
}

// Observer is a manager that lists everything a Snapshot holds in one call per part.
type Observer interface {
	Repos(ctx context.Context) ([]wsdomain.Repo, error)
	Workspaces(ctx context.Context) ([]wsdomain.Workspace, wsdomain.Scope, error)
	Panes(ctx context.Context) ([]wsdomain.Pane, error)
	// AllTerminals lists every terminal of every workspace; Scope.Complete also requires
	// the manager to report itself ready.
	AllTerminals(ctx context.Context) ([]wsdomain.Terminal, wsdomain.Scope, error)
}

// StartRequest starts an agent session in a workspace.
type StartRequest struct {
	WorkspaceID string
	// Session is the id the loop minted for the session; the agent runs under it.
	Session string
	Agent   string
	// Command is the shell line a terminal manager runs to start the agent.
	Command string
	Title   string
	// Model is the agent's model for the session, "" for its default.
	Model string
	// Settings are the session's permission settings as the agent reads them,
	// "" for none; a manager that cannot pass Command's flags writes them where
	// the agent loads them.
	Settings string
}

// Manager is an app that hosts worktrees and the agent sessions in them. What
// only some managers offer is an optional interface below; callers do without it.
type Manager interface {
	// Name is the manager's key in the workspace manager registry, such as "orca".
	Name() string
	// Hosts says whether the manager can run sessions of agent.
	Hosts(agent string) bool
	Reachable(ctx context.Context) error
	Repos(ctx context.Context) ([]wsdomain.Repo, error)
	AddRepo(ctx context.Context, path string) (wsdomain.Repo, error)
	List(ctx context.Context) ([]wsdomain.Workspace, error)
	Create(ctx context.Context, req CreateRequest) (wsdomain.Workspace, string, error)
	AgentActivity(ctx context.Context) (map[string]wsdomain.Activity, error)
	LiveAgentSession(ctx context.Context, workspaceID, agent string) (Session, bool, error)
	// StartAgentSession starts the agent in the workspace and waits until it
	// is ready, returning the handle of its process; a *BlockedError carries
	// the handle of a start that stopped on a prompt.
	StartAgentSession(ctx context.Context, req StartRequest) (string, error)
	// SendPrompt types the prompt and presses Enter. submitted reports whether
	// the agent was observed accepting it.
	// With queued, the agent is mid-turn and takes the prompt when the turn
	// ends, so text reaching the process counts as submitted.
	SendPrompt(ctx context.Context, handle, prompt string, queued bool) (submitted bool, err error)
	// Focus brings the workspace on screen in the app: its live agent session
	// when there is one, else whatever the app shows of the worktree. It fails
	// when the workspace has nothing to show.
	Focus(ctx context.Context, id string) error
	// Remove deletes the workspace and its checkout. The caller checks first
	// that nothing would be lost; force only waives the manager's own check
	// that every process of the workspace exited.
	Remove(ctx context.Context, id string, force bool) error
	// Unregister drops the workspace from the manager without force. It is
	// meant for a workspace whose checkout is already gone.
	Unregister(ctx context.Context, id string) error
}

// AppProbe is a manager whose listings stay readable while its app is down,
// such as worktrees read from disk: Cached asks it, not the snapshot, whether
// the manager can be asked now.
type AppProbe interface {
	AppRunning(ctx context.Context) error
}

// LazyMCP is a manager whose agent connects its MCP servers only when its
// first prompt arrives, so a new session cannot show the loop's MCP tool before
// the loop types into it.
type LazyMCP interface {
	ConnectsMCPOnFirstPrompt() bool
}

// SessionStore is a manager that keeps its own record of each session and
// says when the user closed one there, such as archiving or deleting it.
type SessionStore interface {
	// Closed takes the session's newest handle, else its id.
	Closed(ctx context.Context, session string) (bool, error)
}

// Board is a manager with a board of workspace cards in columns.
type Board interface {
	SetStatus(ctx context.Context, id, status string) error
	MarkUnread(ctx context.Context, id string) error
	// SetCard moves the card to status, sets its comment and marks it unread in
	// one write; an empty status or comment, or a false unread, leaves that part.
	SetCard(ctx context.Context, id, status, comment string, unread bool) error
}

// Linker is a manager that shows the pull request a workspace is for.
type Linker interface {
	LinkUnit(ctx context.Context, id string, u unit.Unit) error
}

// Renamer is a manager whose workspaces carry a display name.
type Renamer interface {
	Rename(ctx context.Context, id, displayName string) error
}

// ScreenReader is a manager that can read what a process renders now.
type ScreenReader interface {
	ReadScreen(ctx context.Context, handle string) (string, error)
}

// TerminalCloser is a manager that can close a terminal the caller started.
type TerminalCloser interface {
	CloseTerminal(ctx context.Context, handle string) error
}

// LaunchTruster is a manager that may trust a worktree in an agent home as it
// launches a session there.
type LaunchTruster interface {
	TrustsAtLaunch(agent, home string) bool
}

// PromptRewriter is a manager that delivers a prompt as a message the agent
// does not expand, so a slash command must be rewritten into words.
type PromptRewriter interface {
	// RewritePrompt is the line the agent receives for prompt.
	RewritePrompt(prompt string) string
}

// WakeRequest wakes a dormant session.
type WakeRequest struct {
	WorkspaceID, Session, Agent string
	// Handle is the newest handle the manager gave the session, "" for none.
	Handle string
	// Settings are the session's permission settings, as in StartRequest.
	Settings string
	// AgentSession is the agent's own id of the conversation to resume.
	AgentSession string
	// Command is the shell line a terminal manager runs to resume it.
	Command string
	Title   string
	// Model is the agent's model for the session, "" for its default.
	Model string
}

// Waker is a manager that can give a dormant session a live process again.
// Wake's handle comes with an error when the process started but is not
// ready, so a process that comes up late lands on the woken session.
type Waker interface {
	Wake(ctx context.Context, req WakeRequest) (handle string, err error)
}

// LaunchNeeds is what a session's launch policy asks of the manager that runs it.
type LaunchNeeds struct {
	// UserSettingsOnly keeps the worktree's own agent settings from loading.
	UserSettingsOnly bool
	// Strict makes the policy the session's only permissions.
	Strict bool
	// NoMCP keeps every MCP server but the loop's from loading.
	NoMCP bool
	// CustomHome is an agent home other than the agent's default.
	CustomHome bool
}

// PolicyHonorer is a manager that honors only some launch policies; one that
// does not implement it honors every policy.
type PolicyHonorer interface {
	// Honors is nil when the manager can launch a session with needs, else why not.
	Honors(needs LaunchNeeds) error
}

// Capability is m's optional feature T, false when m lacks it. On a *Cached the
// feature is the cache's, which invalidates what the feature changes.
func Capability[T any](m Manager) (T, bool) {
	var zero T
	if m == nil {
		return zero, false
	}
	if c, ok := m.(*Cached); ok {
		if _, ok := c.Manager.(T); !ok {
			return zero, false
		}
	}
	t, ok := m.(T)
	return t, ok
}

// Honors is nil when m can launch a session with needs.
func Honors(m Manager, needs LaunchNeeds) error {
	if h, ok := Capability[PolicyHonorer](m); ok {
		return h.Honors(needs)
	}
	return nil
}

// ReasonTrust is the blocked reason of a session on the agent's workspace
// trust dialog.
const ReasonTrust = "agent-trust-workspace"

// BlockedError is an agent session that started but waits on a prompt in
// its terminal, such as the agent's own trust dialog.
type BlockedError struct{ Handle, Reason string }

func (e *BlockedError) Error() string {
	return fmt.Sprintf("the agent session waits on %s in its terminal %s", e.Reason, e.Handle)
}
