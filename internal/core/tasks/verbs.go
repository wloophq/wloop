package tasks

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/task"
)

var (
	todo     = []task.State{task.Inbox, task.Next, task.Waiting, task.Scheduled, task.Someday}
	live     = append(slices.Clone(todo), task.Reference)
	anyState = append(slices.Clone(live), task.Done, task.Trash)
)

func without(ss []task.State, drop ...task.State) []task.State {
	return slices.DeleteFunc(slices.Clone(ss), func(s task.State) bool { return slices.Contains(drop, s) })
}

// moves is the transition table: the states each verb accepts, and the state it moves the entry to ("" keeps it).
// approve, delegate, answer, edit, retry and reply move nothing on their own: a registered handler or the
// daemon runs them.
var moves = map[task.Verb]struct {
	from []task.State
	to   task.State
}{
	task.VerbNext:      {append(without(todo, task.Next), task.Reference), task.Next},
	task.VerbWaiting:   {without(todo, task.Waiting), task.Waiting},
	task.VerbSchedule:  {todo, task.Scheduled},
	task.VerbSomeday:   {without(todo, task.Someday), task.Someday},
	task.VerbReference: {append(slices.Clone(todo), task.Done), task.Reference},
	task.VerbDone:      {live, task.Done},
	task.VerbDismiss:   {append(slices.Clone(live), task.Done), task.Trash},
	task.VerbFocus:     {live, ""},
	task.VerbProject:   {live, ""},
	task.VerbContext:   {live, ""},
	task.VerbArea:      {live, ""},
	task.VerbSeen:      {anyState, ""},
	task.VerbApprove:   {},
	task.VerbDelegate:  {},
	task.VerbAnswer:    {},
	task.VerbEdit:      {},
	task.VerbRetry:     {},
	task.VerbReply:     {},
}

func known(v task.Verb) bool {
	_, ok := moves[v]
	return ok
}

// accepts says whether e's state takes verb, naming both when it does not.
func accepts(e task.Task, v task.Verb) error {
	m, ok := moves[v]
	if !ok {
		return fmt.Errorf("%w: unknown verb %q", ErrArgs, v)
	}
	if !slices.Contains(m.from, e.State) {
		return fmt.Errorf("%w: %s on a %s entry", ErrVerb, v, e.State)
	}
	return nil
}

// transition is the state change of a verb with no handler; project is the service's.
func transition(e task.Task, req Request, now time.Time) (task.Task, error) {
	if err := accepts(e, req.Verb); err != nil {
		return task.Task{}, err
	}
	m := moves[req.Verb]
	switch req.Verb {
	case task.VerbSeen:
		e.Changed, e.SeenAt = false, now
	case task.VerbFocus:
		e.Focus = task.Focus{On: !e.Focus.On, At: now}
	case task.VerbContext:
		e.Contexts = split(req.Args["set"])
	case task.VerbArea:
		e.Area = strings.TrimSpace(req.Args["set"])
	case task.VerbSchedule:
		snooze, err := scheduleArgs(e, req.Args, now)
		if err != nil {
			return task.Task{}, err
		}
		e.Snooze = snooze
	case task.VerbWaiting:
		w, err := waitingArgs(e, req.Args, now)
		if err != nil {
			return task.Task{}, err
		}
		e.Waiting = w
	}
	if m.to != "" {
		if req.Verb != task.VerbSchedule {
			e.Snooze = task.Snooze{Mute: e.Snooze.Mute}
		}
		e.State, e.Changed, e.SeenAt = m.to, false, now
	}
	e.Updated = now
	return e, nil
}

func split(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" && !slices.Contains(out, p) {
			out = append(out, p)
		}
	}
	return out
}

func parseDate(key, s string, now time.Time) (time.Time, error) {
	if s == "" {
		return time.Time{}, nil
	}
	t, ok := task.ParseDeadline(s, now.Location())
	if !ok {
		return time.Time{}, fmt.Errorf("%w: %s %q is neither RFC 3339 nor YYYY-MM-DD", ErrArgs, key, s)
	}
	return t, nil
}

func scheduleArgs(e task.Task, args map[string]string, now time.Time) (task.Snooze, error) {
	until, err := parseDate("until", args["until"], now)
	if err != nil {
		return task.Snooze{}, err
	}
	onActivity := args["on_activity"] == "true"
	if until.IsZero() && !onActivity {
		return task.Snooze{}, fmt.Errorf("%w: schedule needs until or on_activity", ErrArgs)
	}
	ret := e.State
	if e.State == task.Scheduled {
		ret = e.Snooze.Return
	}
	return task.Snooze{Until: until, OnActivity: onActivity, Return: ret}, nil
}

func waitingArgs(e task.Task, args map[string]string, now time.Time) (*task.WaitingOn, error) {
	var w task.WaitingOn
	if e.Waiting != nil {
		w = *e.Waiting
	}
	w.Since = now
	if on := split(args["on"]); len(on) > 0 {
		w.On = nil
		for _, p := range on {
			w.On = append(w.On, items.Person{Key: p, Name: p})
		}
	}
	resurface, err := parseDate("resurface", args["resurface"], now)
	if err != nil {
		return nil, err
	}
	if !resurface.IsZero() {
		w.Resurface = resurface
	}
	return &w, nil
}
