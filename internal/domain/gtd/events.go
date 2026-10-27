package gtd

import "slices"

// Verbs are every verb, in the order clients list them.
var Verbs = []Verb{VerbNext, VerbWaiting, VerbSchedule, VerbSomeday, VerbReference, VerbFocus, VerbProject, VerbApprove,
	VerbDelegate, VerbDone, VerbDismiss, VerbSeen, VerbContext, VerbArea, VerbAnswer, VerbEdit, VerbRetry, VerbReviewed,
	VerbSequential, VerbReply}

// VerbEventKind is the kind of the event a person running v on an entry records.
func VerbEventKind(v Verb) string { return "verb_" + string(v) }

// VerbEventKinds are the kinds of the verbs that act on a stored entry:
// every verb but seen, which clients send on their own when an entry is
// opened, and edit and retry, which act on an Outbox draft.
var VerbEventKinds = func() []string {
	var out []string
	for _, v := range Verbs {
		if !slices.Contains([]Verb{VerbSeen, VerbEdit, VerbRetry}, v) {
			out = append(out, VerbEventKind(v))
		}
	}
	return out
}()
