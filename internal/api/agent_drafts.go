package api

import (
	"encoding/json"
	"time"
)

// AgentDraftProposal is a draft an agent proposes for the user's approval.
type AgentDraftProposal struct {
	// Unit is the unit the draft posts on, "<provider>/<key>"; "" is the unit of the caller's turn.
	Unit string `json:"unit,omitempty"`
	// Kind is a draft kind the unit's plugin declares.
	Kind   string `json:"kind"`
	Target string `json:"target,omitempty"`
	Text   string `json:"text,omitempty"`
	// Payload is what the kind's schema shapes beside Text, posted verbatim.
	Payload json.RawMessage `json:"payload,omitempty"`
	// Basis is what the agent read the unit at, such as a pull request's head, for a kind that takes one.
	Basis string `json:"basis,omitempty"`
	// Hold keeps the draft waiting for the user whatever the approval rules say.
	Hold    bool     `json:"hold,omitempty"`
	Items   []string `json:"items,omitempty"`
	Effects []string `json:"effects,omitempty"`
	Why     string   `json:"why,omitempty"`
}

// AgentDraftUpdate revises draft ID at Rev, the rev the agent read; an absent field is left as it is.
type AgentDraftUpdate struct {
	ID      int64           `json:"id"`
	Rev     int64           `json:"rev"`
	Text    *string         `json:"text,omitempty"`
	Payload json.RawMessage `json:"payload,omitempty"`
	Basis   *string         `json:"basis,omitempty"`
}

// AgentDraftWithdrawal withdraws draft ID at Rev for Reason.
type AgentDraftWithdrawal struct {
	ID     int64  `json:"id"`
	Rev    int64  `json:"rev"`
	Reason string `json:"reason,omitempty"`
}

// AgentDraft is a draft as an agent reads it.
type AgentDraft struct {
	ID       int64           `json:"id"`
	Rev      int64           `json:"rev"`
	Unit     string          `json:"unit"` // "<provider>/<key>"
	Kind     string          `json:"kind"`
	Delivery string          `json:"delivery"`
	Target   string          `json:"target,omitempty"`
	Text     string          `json:"text"`
	Payload  json.RawMessage `json:"payload,omitempty"`
	Basis    string          `json:"basis,omitempty"`
	Hold     bool            `json:"hold,omitempty"`
	// State is where its delivery stands: held, queued, sending, sent, failed or dropped; Error why it
	// failed, Reason why it was dropped.
	State     string `json:"state"`
	Error     string `json:"error,omitempty"`
	Reason    string `json:"reason,omitempty"`
	Permalink string `json:"permalink,omitempty"`
	// Revisions are its content's revisions, oldest first.
	Revisions []AgentDraftRevision `json:"revisions,omitempty"`
}

// AgentDraftRevision is one revision of a draft's content: By is person, session, rule or window, Session
// the agent session of a session's.
type AgentDraftRevision struct {
	Rev     int64           `json:"rev"`
	Text    string          `json:"text"`
	Payload json.RawMessage `json:"payload,omitempty"`
	Basis   string          `json:"basis,omitempty"`
	By      string          `json:"by"`
	Session string          `json:"session,omitempty"`
	At      time.Time       `json:"at"`
}
