package turn

// AgentState is what a workspace manager shows an agent doing.
type AgentState struct{ name string }

var (
	AgentWorking = AgentState{"working"}
	AgentWaiting = AgentState{"waiting"}
	AgentDone    = AgentState{"done"}
)

// ParseAgentState is the state a manager reported as raw; an unknown raw value is kept as is.
func ParseAgentState(raw string) AgentState { return AgentState{raw} }

func (s AgentState) String() string { return s.name }

func (s AgentState) MarshalText() ([]byte, error) { return []byte(s.name), nil }

func (s *AgentState) UnmarshalText(b []byte) error {
	*s = ParseAgentState(string(b))
	return nil
}
