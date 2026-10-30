package task

// VerbEventKind is the kind of the event running v on a task records.
func VerbEventKind(v Verb) string { return "verb_" + string(v) }

// VerbEventKinds are the kinds of the events of verbs, the profile's.
func VerbEventKinds(verbs []string) []string {
	out := make([]string, len(verbs))
	for i, v := range verbs {
		out[i] = VerbEventKind(Verb(v))
	}
	return out
}
