package gtd

// VerbEventKind is the kind of the event a person running v on an entry records.
func VerbEventKind(v Verb) string { return "verb_" + string(v) }

// VerbEventKinds are the kinds of the verbs that act on a stored entry.
// seen is left out: clients send it on their own when an entry is opened.
var VerbEventKinds = func() []string {
	var out []string
	for _, v := range []Verb{VerbNext, VerbWaiting, VerbSchedule, VerbSomeday, VerbReference, VerbFocus, VerbProject,
		VerbApprove, VerbDelegate, VerbDone, VerbDismiss, VerbContext, VerbArea, VerbAnswer, VerbReviewed, VerbSequential, VerbReply} {
		out = append(out, VerbEventKind(v))
	}
	return out
}()
