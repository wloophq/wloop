package claude

import (
	"bytes"
	"encoding/json"
)

const (
	SessionIDVar = "CLAUDE_CODE_SESSION_ID"
	HomeVar      = "CLAUDE_CONFIG_DIR"
)

// Protocol is Claude Code's hook payloads and session environment.
type Protocol struct{}

func (Protocol) SessionID(getenv func(string) string) string { return getenv(SessionIDVar) }

func (Protocol) Home(getenv func(string) string) string { return getenv(HomeVar) }

func (Protocol) SessionStart(raw []byte) (agentSession, source, cwd string, err error) {
	var in struct {
		SessionID string `json:"session_id"`
		Source    string `json:"source"`
		Cwd       string `json:"cwd"`
	}
	err = json.Unmarshal(raw, &in)
	return in.SessionID, in.Source, in.Cwd, err
}

func (Protocol) Stop(raw []byte) (agentSession, promptID, lastMessage string, err error) {
	var in struct {
		SessionID   string `json:"session_id"`
		PromptID    string `json:"prompt_id"`
		LastMessage string `json:"last_assistant_message"`
	}
	err = json.Unmarshal(raw, &in)
	return in.SessionID, in.PromptID, in.LastMessage, err
}

// Prompt is the prompt of a UserPromptSubmit payload.
func (Protocol) Prompt(raw []byte) (string, error) {
	var in struct {
		Prompt string `json:"prompt"`
	}
	err := json.Unmarshal(raw, &in)
	return in.Prompt, err
}

// Transcript is the transcript path of a SessionStart or Stop payload, "" when
// it names none or does not decode.
func (Protocol) Transcript(raw []byte) string {
	var in struct {
		Path string `json:"transcript_path"`
	}
	_ = json.Unmarshal(raw, &in)
	return in.Path
}

// Block is the Stop hook's answer that keeps the session going with reason.
func (Protocol) Block(reason string) []byte {
	var buf bytes.Buffer
	_ = json.NewEncoder(&buf).Encode(map[string]string{"decision": "block", "reason": reason})
	return buf.Bytes()
}
