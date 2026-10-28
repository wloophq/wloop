package taskstest

import (
	"context"
	"slices"

	"github.com/wloophq/wloop/internal/domain/gate"
	"github.com/wloophq/wloop/internal/domain/task"
)

// BundledStates are the bundled profile's states and verb transitions, for tests that run the service with no
// profile at hand.
var BundledStates = bundledStates{}

type bundledStates struct{}

var states = []struct {
	name string
	c    task.Category
}{
	{"inbox", task.CategoryInbox}, {"next", task.CategoryReady}, {"waiting", task.CategoryWaiting},
	{"scheduled", task.CategoryDeferred}, {"someday", task.CategoryDeferred}, {"done", task.CategoryDone},
	{"reference", task.CategoryDone}, {"trash", task.CategoryDropped},
}

var (
	todo = []string{"inbox", "next", "waiting", "scheduled", "someday"}
	live = append(slices.Clone(todo), "reference")
)

func without(ss []string, drop string) []string {
	return slices.DeleteFunc(slices.Clone(ss), func(s string) bool { return s == drop })
}

var moves = map[task.Verb]struct {
	from []string
	to   string
}{
	task.VerbNext:      {append(without(todo, "next"), "reference"), "next"},
	task.VerbWaiting:   {without(todo, "waiting"), "waiting"},
	task.VerbSchedule:  {todo, "scheduled"},
	task.VerbSomeday:   {without(todo, "someday"), "someday"},
	task.VerbReference: {append(slices.Clone(todo), "done"), "reference"},
	task.VerbDone:      {live, "done"},
	task.VerbDismiss:   {append(slices.Clone(live), "done"), "trash"},
	task.VerbFocus:     {live, ""},
	task.VerbProject:   {live, ""},
	task.VerbContext:   {live, ""},
	task.VerbArea:      {live, ""},
}

// InState is the bundled state named s, for fixtures.
func InState(s string) task.State { return task.State(s) }

func (bundledStates) CategoryOf(s string) (task.Category, bool) {
	for _, r := range states {
		if r.name == s {
			return r.c, true
		}
	}
	return "", false
}

func (bundledStates) FirstOf(c task.Category) string {
	for _, r := range states {
		if r.c == c {
			return r.name
		}
	}
	return ""
}

func (bundledStates) Move(verb string) ([]string, string, bool) {
	if m, ok := moves[task.Verb(verb)]; ok {
		return m.from, m.to, true
	}
	return nil, "", slices.Contains(task.Verbs, task.Verb(verb))
}

// handlers are the bundled verbs that name a handler, each the one named as the verb.
var handlers = []task.Verb{task.VerbWaiting, task.VerbSchedule, task.VerbFocus, task.VerbProject, task.VerbApprove,
	task.VerbDelegate, task.VerbSeen, task.VerbContext, task.VerbArea, task.VerbAnswer, task.VerbEdit, task.VerbRetry,
	task.VerbReviewed, task.VerbSequential, task.VerbReply}

// byWorkflow are the bundled verbs a workflow may run; a person may run every verb.
var byWorkflow = []task.Verb{task.VerbDone, task.VerbDismiss, task.VerbNext, task.VerbWaiting, task.VerbSchedule, task.VerbFocus}

func (bundledStates) Handler(verb string) string {
	if slices.Contains(handlers, task.Verb(verb)) {
		return verb
	}
	return ""
}

func (bundledStates) Gate(string) (gate.Gate, bool) { return gate.Gate{}, false }

// Authority lets a person run every bundled verb and a workflow those whose by.workflow is true.
func (bundledStates) Authority(_ context.Context, verb string, a task.Actor, _ task.Task) (bool, bool, error) {
	switch a {
	case task.ActorPerson:
		return slices.Contains(task.Verbs, task.Verb(verb)), false, nil
	case task.ActorWorkflow:
		return slices.Contains(byWorkflow, task.Verb(verb)), false, nil
	}
	return false, false, nil
}
