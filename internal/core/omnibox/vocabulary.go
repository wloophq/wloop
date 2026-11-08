package omnibox

import (
	"cmp"
	"slices"
	"strings"
)

// The operations an omnibox verb runs besides an action or a task verb.
const (
	OpAction  = "action"  // a catalog action on a unit
	OpVerb    = "verb"    // a task verb on a task
	OpCapture = "capture" // a new task titled with the text
	OpStart   = "start"   // a new task from a brief, started at once
)

// Verb is a verb the omnibox knows: the words that name it in any language, and what it runs: a catalog
// Action on each unit named, a TaskVerb on each task named, or Op on the text when nothing is named.
type Verb struct {
	ID                   string
	Words                []string
	Action, TaskVerb, Op string
}

// Vocabulary are the verbs the omnibox runs; no operation runs that none of them targets.
type Vocabulary struct {
	Verbs []Verb
	// Fold is how words compare, case and accents aside; nil compares them lowercased.
	Fold func(string) string
}

func (v Vocabulary) fold(s string) string {
	if v.Fold == nil {
		return strings.ToLower(s)
	}
	return v.Fold(s)
}

// Match is the verb one of whose words is word.
func (v Vocabulary) Match(word string) (Verb, bool) {
	w := v.fold(strings.TrimSpace(word))
	for _, verb := range v.Verbs {
		if slices.ContainsFunc(verb.Words, func(x string) bool { return v.fold(x) == w }) {
			return verb, true
		}
	}
	return Verb{}, false
}

// Allowed is why the vocabulary refuses op, DropOutsideVocabulary naming what it would run, "" when a verb
// targets it.
func (v Vocabulary) Allowed(op AskOp) (why string, params map[string]string) {
	ok := slices.ContainsFunc(v.Verbs, func(verb Verb) bool {
		switch op.Op {
		case OpAction:
			return verb.Action != "" && verb.Action == op.Action
		case OpVerb:
			return verb.TaskVerb != "" && verb.TaskVerb == op.Verb
		case OpCapture, OpStart:
			return verb.Op == op.Op
		}
		return false
	})
	if ok {
		return "", nil
	}
	return DropOutsideVocabulary, map[string]string{"name": cmp.Or(op.Action, op.Verb, op.Op)}
}
