package turn

import (
	"encoding/json"
	"slices"
	"testing"
)

func TestParseAgentStateGivesTheKnownValues(t *testing.T) {
	if got := ParseAgentState("done"); got != AgentDone {
		t.Fatalf("done parsed as %v", got)
	}
}

func TestAnUnknownAgentStateKeepsItsName(t *testing.T) {
	if got := ParseAgentState("idle").String(); got != "idle" {
		t.Fatalf("idle reads %q", got)
	}
}

func TestAgentStatesRoundTripAsJSONStrings(t *testing.T) {
	states := []AgentState{AgentWorking, ParseAgentState("idle")}
	raw, err := json.Marshal(states)
	if err != nil || string(raw) != `["working","idle"]` {
		t.Fatalf("json %s, %v", raw, err)
	}
	var back []AgentState
	if err := json.Unmarshal(raw, &back); err != nil || !slices.Equal(back, states) {
		t.Fatalf("decoded %v, %v", back, err)
	}
}
