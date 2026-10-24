package prrow

import (
	"encoding/json"
	"testing"

	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/domain/turn"
)

func TestWireKeepsAnUnknownAgentState(t *testing.T) {
	w := Workspace(dinv.Workspace{ID: "w", AgentStates: []turn.AgentState{turn.AgentWorking, turn.ParseAgentState("idle")}})
	raw, err := json.Marshal(w.AgentStates)
	if err != nil || string(raw) != `["working","idle"]` {
		t.Fatalf("wire %s, %v", raw, err)
	}
}
