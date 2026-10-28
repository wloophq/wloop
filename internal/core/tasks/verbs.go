package tasks

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/task"
)

// The handlers the service runs itself as part of a verb's move; a verb
// with another handler moves nothing on its own: a registered handler or
// the daemon runs it.
const (
	handlerSeen     = "seen"
	handlerFocus    = "focus"
	handlerContext  = "context"
	handlerArea     = "area"
	handlerSchedule = "schedule"
	handlerWaiting  = "waiting"
	handlerProject  = "project"
)

var ownHandlers = []string{handlerSeen, handlerFocus, handlerContext, handlerArea, handlerSchedule, handlerWaiting, handlerProject}

// Accepts says whether actor a may run verb v on t as its move: ErrArgs for a verb the profile does not
// declare, ErrVerb naming the verb and the state for one t's state refuses or one whose handler the service
// does not run, ErrForbidden for one the profile's by does not let a run. A verb whose by opens a gate for a is
// accepted: running it opens the gate.
func (s *Service) Accepts(t task.Task, v task.Verb, a task.Actor) error {
	if err := s.accepts(t, v); err != nil {
		return err
	}
	_, err := s.Allowed(context.Background(), t, Request{Verb: v, Actor: a})
	return err
}

// declared refuses a verb the profile does not declare, and one t's state is not among the verb's from.
func (s *Service) declared(e task.Task, v task.Verb) error {
	from, _, ok := s.d.States.Move(string(v))
	if !ok {
		return fmt.Errorf("%w: unknown verb %q", ErrArgs, v)
	}
	if len(from) > 0 && !slices.Contains(from, string(e.State)) {
		return fmt.Errorf("%w: %s on a %s entry", ErrVerb, v, e.State)
	}
	return nil
}

// accepts says whether e's state takes verb as a move the service runs, as the profile's from and handler say,
// naming both when it does not.
func (s *Service) accepts(e task.Task, v task.Verb) error {
	if err := s.declared(e, v); err != nil {
		return err
	}
	if h := s.d.States.Handler(string(v)); h != "" && !slices.Contains(ownHandlers, h) {
		return fmt.Errorf("%w: %s on a %s entry", ErrVerb, v, e.State)
	}
	return nil
}

// allowed says whether a may run v on e, and whether running it opens v's gate instead, as the profile's
// authority says.
func (s *Service) allowed(ctx context.Context, e task.Task, v task.Verb, a task.Actor) (gated bool, err error) {
	allow, gate, err := s.authority(ctx, string(v), a, e)
	switch {
	case err != nil:
		return false, err
	case gate:
		if _, ok := s.d.States.Gate(string(v)); !ok {
			return false, fmt.Errorf("%w: %s opens a gate the profile does not name", ErrVerb, v)
		}
		return true, nil
	case !allow:
		return false, fmt.Errorf("%w: %s may not run %s on this entry", ErrForbidden, a, v)
	}
	return false, nil
}

// transition is the state change of a verb with no handler, or one the service runs, to the state the profile's
// to names; project is the service's.
func (s *Service) transition(e task.Task, req Request, now time.Time) (task.Task, error) {
	if err := s.accepts(e, req.Verb); err != nil {
		return task.Task{}, err
	}
	_, to, _ := s.d.States.Move(string(req.Verb))
	switch s.d.States.Handler(string(req.Verb)) {
	case handlerSeen:
		e.Changed, e.SeenAt = false, now
	case handlerFocus:
		v := task.FocusOn
		if e.Starred() {
			v = task.FocusOff
		}
		e.SetProp(task.PropFocus, task.Value{Text: v}, task.Writer(req.Actor), now)
	case handlerContext:
		e.SetProp(task.PropContexts, task.Value{List: split(req.Args["set"])}, task.Writer(req.Actor), now)
	case handlerArea:
		e.SetProp(task.PropArea, task.Value{Text: strings.TrimSpace(req.Args["set"])}, task.Writer(req.Actor), now)
	case handlerSchedule:
		snooze, err := scheduleArgs(e, req.Args, now)
		if err != nil {
			return task.Task{}, err
		}
		e.Snooze = snooze
	case handlerWaiting:
		w, err := waitingArgs(e, req.Args, now)
		if err != nil {
			return task.Task{}, err
		}
		e.Waiting = w
	}
	if to != "" {
		if s.d.States.Handler(string(req.Verb)) != handlerSchedule {
			e.Snooze = task.Snooze{Mute: e.Snooze.Mute}
		}
		e.Become(task.State(to), s.d.States)
		e.Changed, e.SeenAt = false, now
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
	if e.Snoozed() {
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
