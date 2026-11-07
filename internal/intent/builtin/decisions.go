package builtin

import "github.com/wloophq/wloop/internal/domain/decision"

// SurfaceDecisions is decisions.toml, of the default and the plugin layers.
const SurfaceDecisions Surface = "decisions"

var decisionDocs = map[decision.Kind]string{
	decision.Answer:      "An agent's question or a post waits for the user's answer.",
	decision.ApprovePlan: "An agent's plan waits for the user's approval.",
	decision.SendDraft:   "A draft waits for the user to send, edit or drop it.",
	decision.ReviewStep:  "A step of a review waits for the user's look.",
	decision.LastStep:    "A unit waits only for its last step at its source, such as a merge or a closure, which the user takes there.",
	decision.PickSomeone: "A unit waits for the user to pick who takes it next, such as a reviewer.",
	decision.Act:         "A proposal or a trust request waits for the user to act on it.",
}

// DecisionKinds are the decision kinds as enum values, in decision.Kinds order: what decisions.toml maps gate
// kinds, situations and signals to.
func DecisionKinds() []Value {
	out := make([]Value, 0, len(decision.Kinds))
	for _, k := range decision.Kinds {
		out = append(out, Value{Name: string(k), Doc: decisionDocs[k]})
	}
	return out
}
