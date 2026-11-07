// Package decision is the closed vocabulary of the decisions a task waits on the user for.
package decision

import "slices"

// Kind is a kind of decision.
type Kind string

// The decision kinds; a new kind may be added, none renamed.
const (
	Answer      Kind = "answer"
	ApprovePlan Kind = "approve-plan"
	SendDraft   Kind = "send-draft"
	ReviewStep  Kind = "review-step"
	LastStep    Kind = "last-step"
	PickSomeone Kind = "pick-someone"
	Act         Kind = "act"
)

// Kinds are every kind, in the order the home and the queues list them.
var Kinds = []Kind{Answer, ApprovePlan, SendDraft, ReviewStep, LastStep, PickSomeone, Act}

// Valid says whether k is one of Kinds.
func Valid(k Kind) bool { return slices.Contains(Kinds, k) }
