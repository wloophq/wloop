package prrow

import (
	"github.com/wloophq/wloop/internal/api"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/domain/turn"
)

// Workspace is the wire form of a pull request's workspace.
func Workspace(w dinv.Workspace) api.Workspace {
	return api.Workspace{ID: w.ID, DisplayName: w.DisplayName, Status: w.Status, IsReview: w.IsReview,
		AgentStates: agentStates(w.AgentStates), Comment: w.Comment, IsMain: w.IsMain}
}

func agentStates(states []turn.AgentState) []string {
	if states == nil {
		return nil
	}
	out := make([]string, 0, len(states))
	for _, s := range states {
		out = append(out, s.String())
	}
	return out
}

// Override is the wire form of a stored override.
func Override(o dinv.Override) api.Override {
	return api.Override{Hold: o.Hold, Adopted: o.Adopted, Tracked: o.Tracked, NextGuidance: o.NextGuidance}
}
