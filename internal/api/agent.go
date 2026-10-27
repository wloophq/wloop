package api

import (
	"encoding/json"
	"time"
)

// AgentAPI paths, served on the Unix socket only. The caller's session is
// the SessionHeader.
const (
	AgentPrefix         = "/api/agent/"
	AgentTurnsStart     = "/api/agent/turns/start"
	AgentTurnsByHand    = "/api/agent/turns/by-hand"
	AgentTurnsEnd       = "/api/agent/turns/end"
	AgentSessionsStart  = "/api/agent/sessions/start"
	AgentSessionsStop   = "/api/agent/sessions/stop"
	AgentSessionsPrompt = "/api/agent/sessions/prompt"
	AgentAnswer         = "/api/agent/answer"
)

type TurnStartRequest struct {
	Turn   string `json:"turn"`
	Intent string `json:"intent"`
}

// TurnText is a turn's stored instructions, or the answers the caller reads
// with turn answer.
type TurnText struct {
	Text string `json:"text"`
}

// ByHandRequest is a turn the user starts by hand from the session that
// calls, described by that session's environment.
type ByHandRequest struct {
	Intent string `json:"intent"`
	URL    string `json:"url,omitempty"`
	// Inputs are the intent's declared inputs, by name.
	Inputs       map[string]any `json:"inputs,omitempty"`
	Agent        string         `json:"agent,omitempty"`
	Guidance     string         `json:"guidance,omitempty"`
	Cwd          string         `json:"cwd"`
	Pane         string         `json:"pane,omitempty"`
	AgentSession string         `json:"agent_session,omitempty"`
	Home         string         `json:"home,omitempty"`
}

type ByHandResult struct {
	Text string `json:"text"`
	// Notes are what the loop did around the turn, for the caller's standard
	// error.
	Notes []string `json:"notes,omitempty"`
}

type TurnEndRequest struct {
	Turn string `json:"turn"`
	// PR is the pull request the command line names, "" for none.
	PR           string          `json:"pr,omitempty"`
	Record       json.RawMessage `json:"record"`
	AgentSession string          `json:"agent_session,omitempty"`
	// WrittenAt is when the record's file was last written; zero when the
	// record came from no file.
	WrittenAt time.Time `json:"written_at,omitzero"`
}

type TurnEnded struct {
	PR    string `json:"pr"`
	State string `json:"state"`
	Added bool   `json:"added"`
	// Note tells the agent its record changes nothing.
	Note   string       `json:"note,omitempty"`
	Ledger *ItemsResult `json:"ledger,omitempty"`
}

type SessionStartRequest struct {
	Pane         string `json:"pane,omitempty"`
	WorktreeID   string `json:"worktree_id,omitempty"`
	AgentSession string `json:"agent_session,omitempty"`
	Source       string `json:"source,omitempty"`
	Cwd          string `json:"cwd,omitempty"`
	Home         string `json:"home,omitempty"`
	// Agent is the hook's agent and Transcript the session's transcript
	// file, "" when the agent's payload names none.
	Agent      string `json:"agent,omitempty"`
	Transcript string `json:"transcript,omitempty"`
}

type StopRequest struct {
	AgentSession string `json:"agent_session,omitempty"`
	PromptID     string `json:"prompt_id,omitempty"`
	LastMessage  string `json:"last_message,omitempty"`
	// Agent and Transcript are as in SessionStartRequest.
	Agent      string `json:"agent,omitempty"`
	Transcript string `json:"transcript,omitempty"`
}

// PromptRequest is the prompt the caller's session took.
type PromptRequest struct {
	Prompt string `json:"prompt"`
}

type StopAnswer struct {
	Block  bool   `json:"block"`
	Reason string `json:"reason,omitempty"`
}
