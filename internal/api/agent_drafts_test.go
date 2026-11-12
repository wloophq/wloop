package api

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"testing"
	"time"
)

var update = flag.Bool("update", false, "rewrite the golden files")

func TestAgentDraftsWireIsGolden(t *testing.T) {
	t.Parallel()
	text, basis := "Moved the finding to the body.", "4f9c2d1"
	at := time.Date(2026, 3, 2, 14, 5, 0, 0, time.UTC)
	review := json.RawMessage(`{"event":"REQUEST_CHANGES","comments":[{"path":"api/handler.go","line":42,"body":"This drops the error."}]}`)
	for name, v := range map[string]any{
		"proposal": AgentDraftProposal{Unit: "github/acme/api#7", Kind: "review", Text: "Two blocking findings.", Payload: review,
			Basis: basis, Hold: true, Why: "The handler drops errors."},
		"update":     AgentDraftUpdate{ID: 42, Rev: 3, Text: &text, Basis: &basis},
		"withdrawal": AgentDraftWithdrawal{ID: 43, Rev: 1, Reason: "other_option"},
		"draft": AgentDraft{ID: 42, Rev: 4, Unit: "github/acme/api#7", Kind: "review", Delivery: "review", Text: text, Payload: review,
			Basis: basis, Hold: true, State: "held", Revisions: []AgentDraftRevision{
				{Rev: 1, Text: "Two blocking findings.", Payload: review, Basis: "1a2b3c4", By: "session", Session: "s-octocat-1", At: at},
				{Rev: 4, Text: text, Payload: review, Basis: basis, By: "session", Session: "s-octocat-1", At: at.Add(time.Hour)},
			}},
	} {
		got, err := json.MarshalIndent(v, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, '\n')
		path := filepath.Join("testdata", "agent_drafts_"+name+".golden")
		if *update {
			if err := os.WriteFile(path, got, 0o644); err != nil {
				t.Fatal(err)
			}
			continue
		}
		want, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != string(want) {
			t.Errorf("%s:\n%s\nwant\n%s", path, got, want)
		}
	}
}
