package task

import (
	"strconv"
	"strings"
	"time"
)

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

// The events of a task's own unit the tasks service records: EventCreated when sync derives the task, or a
// person captures one on a unit (a capture with no unit, and a task another service opens, record none);
// EventLinked when a link lands on an item the task holds.
const (
	EventCreated = "task_created"
	EventLinked  = "task_linked"
)

// PairKey is the key of the event a verb that pairs a task with other records at, unique per run: other's id,
// then the time.
func PairKey(other ID, at time.Time) string {
	return string(other) + "/" + strconv.FormatInt(at.UnixNano(), 10)
}

// PairOfKey is the other task of an event PairKey keyed; false for a key it did not write.
func PairOfKey(key string) (ID, bool) {
	id, at, ok := strings.Cut(key, "/")
	if !ok || id == "" {
		return "", false
	}
	if _, err := strconv.ParseInt(at, 10, 64); err != nil {
		return "", false
	}
	return ID(id), true
}
