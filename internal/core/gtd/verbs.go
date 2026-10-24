package gtd

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/wloophq/wloop/internal/domain/gtd"
	"github.com/wloophq/wloop/internal/domain/items"
)

var (
	todo     = []gtd.State{gtd.Inbox, gtd.Next, gtd.Waiting, gtd.Scheduled, gtd.Someday}
	live     = append(slices.Clone(todo), gtd.Reference)
	anyState = append(slices.Clone(live), gtd.Done, gtd.Trash)
)

func without(ss []gtd.State, drop ...gtd.State) []gtd.State {
	return slices.DeleteFunc(slices.Clone(ss), func(s gtd.State) bool { return slices.Contains(drop, s) })
}

// moves is the transition table: the states each verb accepts, and the state it moves the entry to ("" keeps it).
// approve, delegate, answer, edit, retry and reply move nothing on their own: a registered handler or the
// daemon runs them.
var moves = map[gtd.Verb]struct {
	from []gtd.State
	to   gtd.State
}{
	gtd.VerbNext:      {append(without(todo, gtd.Next), gtd.Reference), gtd.Next},
	gtd.VerbWaiting:   {without(todo, gtd.Waiting), gtd.Waiting},
	gtd.VerbSchedule:  {todo, gtd.Scheduled},
	gtd.VerbSomeday:   {without(todo, gtd.Someday), gtd.Someday},
	gtd.VerbReference: {append(slices.Clone(todo), gtd.Done), gtd.Reference},
	gtd.VerbDone:      {live, gtd.Done},
	gtd.VerbDismiss:   {append(slices.Clone(live), gtd.Done), gtd.Trash},
	gtd.VerbFocus:     {live, ""},
	gtd.VerbProject:   {live, ""},
	gtd.VerbContext:   {live, ""},
	gtd.VerbArea:      {live, ""},
	gtd.VerbSeen:      {anyState, ""},
	gtd.VerbApprove:   {},
	gtd.VerbDelegate:  {},
	gtd.VerbAnswer:    {},
	gtd.VerbEdit:      {},
	gtd.VerbRetry:     {},
	gtd.VerbReply:     {},
}

func known(v gtd.Verb) bool {
	_, ok := moves[v]
	return ok
}

// accepts says whether e's state takes verb, naming both when it does not.
func accepts(e gtd.Entry, v gtd.Verb) error {
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
func transition(e gtd.Entry, req Request, now time.Time) (gtd.Entry, error) {
	if err := accepts(e, req.Verb); err != nil {
		return gtd.Entry{}, err
	}
	m := moves[req.Verb]
	switch req.Verb {
	case gtd.VerbSeen:
		e.Changed, e.SeenAt = false, now
	case gtd.VerbFocus:
		e.Focus = gtd.Focus{On: !e.Focus.On, At: now}
	case gtd.VerbContext:
		e.Contexts = split(req.Args["set"])
	case gtd.VerbArea:
		e.Area = strings.TrimSpace(req.Args["set"])
	case gtd.VerbSchedule:
		snooze, err := scheduleArgs(e, req.Args, now)
		if err != nil {
			return gtd.Entry{}, err
		}
		e.Snooze = snooze
	case gtd.VerbWaiting:
		w, err := waitingArgs(e, req.Args, now)
		if err != nil {
			return gtd.Entry{}, err
		}
		e.Waiting = w
	}
	if m.to != "" {
		if req.Verb != gtd.VerbSchedule {
			e.Snooze = gtd.Snooze{Mute: e.Snooze.Mute}
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
	t, ok := gtd.ParseDeadline(s, now.Location())
	if !ok {
		return time.Time{}, fmt.Errorf("%w: %s %q is neither RFC 3339 nor YYYY-MM-DD", ErrArgs, key, s)
	}
	return t, nil
}

func scheduleArgs(e gtd.Entry, args map[string]string, now time.Time) (gtd.Snooze, error) {
	until, err := parseDate("until", args["until"], now)
	if err != nil {
		return gtd.Snooze{}, err
	}
	onActivity := args["on_activity"] == "true"
	if until.IsZero() && !onActivity {
		return gtd.Snooze{}, fmt.Errorf("%w: schedule needs until or on_activity", ErrArgs)
	}
	ret := e.State
	if e.State == gtd.Scheduled {
		ret = e.Snooze.Return
	}
	return gtd.Snooze{Until: until, OnActivity: onActivity, Return: ret}, nil
}

func waitingArgs(e gtd.Entry, args map[string]string, now time.Time) (*gtd.WaitingOn, error) {
	var w gtd.WaitingOn
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
