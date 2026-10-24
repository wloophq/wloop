// Package harness is what a client reads from the coding agent and the
// workspace manager that run it: the agent's hook payloads and the session's
// identity in its environment.
package harness

import (
	"fmt"
	"maps"
	"slices"
	"strings"
)

// SessionEnv reads, from a session's environment, the identity its workspace manager gives it.
type SessionEnv interface {
	Handle(getenv func(string) string) string
	Pane(getenv func(string) string) string
	WorktreeID(getenv func(string) string) string
}

// Claimer is a SessionEnv whose manager also runs sessions the loop does not
// drive: it claims a session only in a working directory it hands the loop.
type Claimer interface {
	Claims(cwd string) bool
}

// Protocol is one coding agent's hook payloads and session environment.
type Protocol interface {
	SessionID(getenv func(string) string) string
	Home(getenv func(string) string) string
	SessionStart(raw []byte) (agentSession, source, cwd string, err error)
	Stop(raw []byte) (agentSession, promptID, lastMessage string, err error)
	Block(reason string) []byte
}

// Transcripts is a Protocol whose hook payloads name the session's
// transcript file.
type Transcripts interface {
	Transcript(raw []byte) string
}

// Prompts is a Protocol that reads the prompt of its prompt hook's payload.
type Prompts interface {
	Prompt(raw []byte) (string, error)
}

// Set is the session environments and agent protocols a client can read, by name.
type Set struct {
	SessionEnvs map[string]SessionEnv
	Agents      map[string]Protocol
	// DefaultAgent is the agent used when the environment names none.
	DefaultAgent string
	// Cwd is the session's working directory; nil reads none, and a Claimer
	// then claims nothing.
	Cwd func() string
}

// Session is the manager whose Handle the environment sets, the first by
// sorted name, with its name; nil and "" when no manager runs the session.
func (s Set) Session(getenv func(string) string) SessionEnv {
	_, env := s.Manager(getenv)
	return env
}

// Manager is Session with the manager's name.
func (s Set) Manager(getenv func(string) string) (string, SessionEnv) {
	for _, name := range slices.Sorted(maps.Keys(s.SessionEnvs)) {
		env := s.SessionEnvs[name]
		if env.Handle(getenv) == "" {
			continue
		}
		if c, ok := env.(Claimer); ok && (s.Cwd == nil || !c.Claims(s.Cwd())) {
			continue
		}
		return name, env
	}
	return "", nil
}

// Handle is the handle the environment sets, the first by sorted manager
// name, claimed or not; "" when no manager sets one.
func (s Set) Handle(getenv func(string) string) string {
	for _, name := range slices.Sorted(maps.Keys(s.SessionEnvs)) {
		if h := s.SessionEnvs[name].Handle(getenv); h != "" {
			return h
		}
	}
	return ""
}

// For is kind's protocol; "" is DefaultAgent's.
func (s Set) For(kind string) (Protocol, error) {
	if kind == "" {
		kind = s.DefaultAgent
	}
	p, ok := s.Agents[kind]
	if !ok {
		return nil, fmt.Errorf("unknown agent %q; known: %s", kind, strings.Join(slices.Sorted(maps.Keys(s.Agents)), ", "))
	}
	return p, nil
}

// Detect is Kind's protocol.
func (s Set) Detect(getenv func(string) string) Protocol { return s.Agents[s.Kind(getenv)] }

// Kind is the agent whose SessionID the environment sets, the first by sorted name, else DefaultAgent.
func (s Set) Kind(getenv func(string) string) string {
	for _, name := range slices.Sorted(maps.Keys(s.Agents)) {
		if s.Agents[name].SessionID(getenv) != "" {
			return name
		}
	}
	return s.DefaultAgent
}
