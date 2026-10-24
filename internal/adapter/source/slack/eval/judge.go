package eval

import (
	_ "embed"
	"encoding/json"
	"strings"
)

//go:embed judge.md
var judgeTemplate string

//go:embed judge.schema.json
var judgeSchema string

// Causes are the ledger root causes a judge may name.
var Causes = []string{"missing_item", "spurious_item", "wrong_assignee", "wrong_waiting_on", "wrong_state", "wrong_resolution",
	"missing_link", "vague_summary", "missing_context", "wrong_verdict", "other"}

type Judgment struct {
	SameActions      bool               `json:"same_actions"`
	Severity         string             `json:"severity"`
	Sections         map[string]Section `json:"sections"`
	ReferenceErrors  []string           `json:"reference_errors"`
	LedgerRootCauses []RootCause        `json:"ledger_root_causes"`
	PromptFixHints   []string           `json:"prompt_fix_hints"`
}

type Section struct {
	ReferenceCount int      `json:"reference_count"`
	Matched        int      `json:"matched"`
	Missed         []string `json:"missed"`
	Extra          []string `json:"extra"`
	Contradicted   []string `json:"contradicted"`
}

type RootCause struct {
	Cause  string `json:"cause"`
	Detail string `json:"detail"`
}

// JudgePrompt asks to compare the blind briefing with the reference.
func JudgePrompt(base map[string]string, conversation string, reference, blind json.RawMessage, toolOutput string) string {
	return fill(judgeTemplate, with(base, "CONVERSATION", conversation, "REFERENCE", indent(reference),
		"TOOL_OUTPUT", toolOutput, "BLIND", indent(blind)))
}

func indent(raw json.RawMessage) string {
	var v any
	if json.Unmarshal(raw, &v) != nil {
		return string(raw)
	}
	out, _ := marshal(v)
	return strings.TrimRight(string(out), "\n")
}
