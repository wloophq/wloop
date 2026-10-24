package readmodel

import "github.com/wloophq/wloop/internal/core/turns"

// AgentsFunc adapts a function to Agents.
type AgentsFunc func() ([]turns.AgentObservation, bool)

func (f AgentsFunc) Agents() ([]turns.AgentObservation, bool) { return f() }
