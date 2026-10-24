package echo

import "encoding/json"

const (
	SessionVar = "ECHO_SESSION"
	HomeVar    = "ECHO_HOME"
)

// Protocol is the echo agent's hook payloads and session environment.
type Protocol struct{}

type payload struct {
	ID     string `json:"id"`
	Prompt string `json:"prompt"`
	Last   string `json:"last"`
}

func (Protocol) SessionID(getenv func(string) string) string { return getenv(SessionVar) }

func (Protocol) Home(getenv func(string) string) string { return getenv(HomeVar) }

func (Protocol) SessionStart(raw []byte) (agentSession, source, cwd string, err error) {
	var p payload
	err = json.Unmarshal(raw, &p)
	return p.ID, "startup", "", err
}

func (Protocol) Stop(raw []byte) (agentSession, promptID, lastMessage string, err error) {
	var p payload
	err = json.Unmarshal(raw, &p)
	return p.ID, p.Prompt, p.Last, err
}

func (Protocol) Block(reason string) []byte { return []byte("BLOCK " + reason + "\n") }
