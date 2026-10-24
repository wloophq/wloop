package turns

import (
	"context"
	"time"

	"github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/workflow"
)

// Terminal is a live process of a workspace, with the session that runs in it,
// "" for a process no session took; a session whose process the manager no
// longer lists is dormant.
type Terminal struct {
	// Key joins the terminal to the agents running in it; opaque, set by the adapter.
	Session, Key, Title, Agent string
	// Manager and Handle address the process.
	Manager, Handle string
	LastOutput      time.Time
}

// AgentObservation is what the app sees an agent doing, a signal and never a turn's state.
type AgentObservation struct {
	// Pane is an opaque key of the agent's pane, equal to turn.Session.Pane for the same pane.
	// TerminalKey equals the Key of the terminal the agent runs in, "" when unknown.
	WorkspaceID, Pane, TerminalKey string
	State                          turn.AgentState
	// StateSince is when the agent entered State, zero when unknown; a repaint does not move it.
	StateSince  time.Time
	LastMessage string
	// UserActive means the workspace is the one open in the app.
	UserActive bool
}

// Sessions are the live processes of the loop's sessions, addressed by session id.
type Sessions interface {
	Terminals(ctx context.Context, workspaceID string) ([]Terminal, error)
	Agents(ctx context.Context) ([]AgentObservation, error)
	Type(ctx context.Context, session, line string) (submitted bool, err error)
	// Census lists every session with a live process, in any state.
	Census(ctx context.Context) (Census, error)
	// Close ends the session's process; a session with none is not an error.
	Close(ctx context.Context, session string) error
}

// Census is every session with a live process, with its workspace id.
type Census struct {
	Sessions map[string]string
	// Complete is false when the list may leave processes out: cut short, a host
	// left out, or a manager not ready. CompleteFor is per manager.
	Complete    bool
	CompleteFor map[string]bool
	// RuntimeID names the manager instance that answered; another one after a
	// restart may list other handles. RuntimeIDs is per manager.
	RuntimeID  string
	RuntimeIDs map[string]string
	At         time.Time
	// Processes address each listed session's process.
	Processes map[string]ProcessRef
}

// ProcessRef is a process's manager and handle.
type ProcessRef struct{ Manager, Handle string }

// CompleteOf says whether the census lists every process of manager.
func (c Census) CompleteOf(manager string) bool {
	if done, ok := c.CompleteFor[manager]; ok {
		return done
	}
	return c.Complete
}

// RuntimeOf is the runtime that answered for manager.
func (c Census) RuntimeOf(manager string) string {
	if id, ok := c.RuntimeIDs[manager]; ok {
		return id
	}
	return c.RuntimeID
}

// LastAgents is a Sessions whose agents can be read from its last snapshot,
// without asking the manager; ok is false when they are unknown.
type LastAgents interface {
	LastAgents() (agents []AgentObservation, ok bool)
}

// Refresher is a Sessions whose reads come from a snapshot: Refresh marks its agents
// and terminals stale and starts reloading them without waiting.
type Refresher interface{ Refresh() }

// refresh makes the next agents and terminals reads see the manager's state from
// now on.
func (s *Service) refresh() {
	if r, ok := s.Sessions.(Refresher); ok {
		r.Refresh()
	}
}

// IdleWaiter is a Sessions that can wait, up to timeout, for an agent's
// prompt to take input again.
type IdleWaiter interface {
	WaitIdle(ctx context.Context, handle string, timeout time.Duration) (idle bool, err error)
}

// saveAgentSession only feeds the usage job, so a failure is logged and
// never fails the hook that reported the session.
func (s *Service) saveAgentSession(ctx context.Context, a AgentSessionInfo) {
	if s.AgentSessions == nil || a.ID == "" {
		return
	}
	a.At = s.now()
	if err := s.AgentSessions.SaveAgentSession(ctx, a); err != nil {
		s.logf("saving the agent session %s of %s: %v", a.ID, a.Session, err)
	}
}

// agentSession is the agent session handle's session runs now, "" when
// unknown or unreadable.
func (s *Service) agentSession(ctx context.Context, handle string) string {
	if handle == "" {
		return ""
	}
	sess, _, err := s.Registry.Session(ctx, handle)
	if err != nil {
		s.logf("reading the agent session of %s: %v", handle, err)
		return ""
	}
	return sess.AgentSession
}

func (s *Service) logf(format string, args ...any) {
	if s.Log != nil {
		s.Log(format, args...)
	}
}

type Launch struct {
	Unit     unit.Unit
	Intent   turn.Intent
	ResumeOf int64
	Answer   string
	Guidance string
	Inputs   map[string]any
	// Trigger is a new turn's; a resume inherits the trigger of the turn it
	// resumes and ignores this.
	Trigger turn.Trigger
	// WorkspaceID, Home and Model are set by the probe: run in this
	// workspace, with this agent home and model, instead of the intent's rules.
	WorkspaceID, Home, Model string
	// Why is the workflow that started the turn; nil for a turn started by hand.
	Why *workflow.Why
}

// Launcher runs a turn the core asks for (a resume or a Continue) through the
// round code: it ensures the workspace, picks or starts a turn-capable
// session, renders and sends the turn through Service.Send.
type Launcher interface {
	Launch(ctx context.Context, l Launch) error
}
