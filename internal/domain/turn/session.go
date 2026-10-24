package turn

import (
	"time"

	"github.com/wloophq/wloop/internal/domain/unit"
)

// Session is an agent's durable conversation in one worktree, identified by the
// id the loop minted for it. Manager owns it; Handle is the manager's address of
// its live process, "" while the session is dormant.
type Session struct {
	ID, Pane, AgentSession, WorkspaceID, Worktree, Home string
	Manager, Handle, Agent                              string
	// LastHandle is the newest handle the manager gave the session, open or
	// ended: a dormant session's address to wake it by.
	LastHandle                            string
	Managed                               bool
	StartedAt, MCPAt, IdleSince, ClosedAt time.Time
	// TypedAt and TypedLine are the loop's last turn typed into the session;
	// the prompt hook sets PromptSeenAt on every prompt and UserPromptAt on one
	// that is not that line.
	TypedAt      time.Time
	TypedLine    string
	UserPromptAt time.Time
	PromptSeenAt time.Time
	Unit         unit.Unit `json:",omitzero"`
}

// MCPGrace is how long before a session's latest start its MCP initialize may
// arrive and still count for that start.
const MCPGrace = 15 * time.Second

// Capable says whether the session's current process has the loop's MCP
// server: an initialize no earlier than MCPGrace before its latest start.
func (s Session) Capable() bool {
	return !s.MCPAt.IsZero() && !s.StartedAt.After(s.MCPAt.Add(MCPGrace))
}

type SessionState string

const (
	// SessionActive has a live process.
	SessionActive SessionState = "active"
	// SessionDormant has no process; its manager can wake it.
	SessionDormant SessionState = "dormant"
	// SessionClosed is over: its worktree or transcript is gone, or it was closed on purpose.
	SessionClosed SessionState = "closed"
)

func (s Session) State() SessionState {
	switch {
	case !s.ClosedAt.IsZero():
		return SessionClosed
	case s.Handle != "":
		return SessionActive
	}
	return SessionDormant
}

// SessionCodec is the unit codec of provider "session".
type SessionCodec struct{}

func (SessionCodec) Provider() unit.ProviderID { return unit.Session }

func (SessionCodec) Valid(key string) error { return unit.OpaqueCodec{ID: unit.Session}.Valid(key) }
