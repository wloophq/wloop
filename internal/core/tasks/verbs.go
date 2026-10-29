package tasks

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/wake"
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
	handlerWake     = "wake"
	handlerPromise  = "promise"
	// HandlerFinish and HandlerDrop are a move and nothing else, unless a kind registers its own handler of
	// them.
	HandlerFinish = "finish"
	HandlerDrop   = "drop"
)

var ownHandlers = []string{handlerSeen, handlerFocus, handlerContext, handlerArea, handlerSchedule, handlerWaiting, handlerProject, handlerWake,
	handlerPromise, HandlerFinish, HandlerDrop}

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

// toggleStar sets the property of the star role to its first value, starred, or to its second when it holds
// the first; ErrVerb when the profile gives no property the star.
func (s *Service) toggleStar(e *task.Task, req Request, now time.Time) error {
	name, values := s.d.States.PropOf(task.RoleStar)
	if name == "" || len(values) < 2 {
		return fmt.Errorf("%w: %s, and no property of tasks.toml takes the star role", ErrVerb, req.Verb)
	}
	v := values[0]
	if e.Prop(name).Text == values[0] {
		v = values[1]
	}
	e.SetProp(name, task.Value{Text: v}, task.Writer(req.Actor), now)
	return nil
}

// wakeChange is what a verb does to its task's wake: puts one, drops it, or neither.
type wakeChange struct {
	put  *wake.Wake
	drop bool
}

// transition is the state change of a verb with no handler, or one the service runs, to the state the profile's
// to names; project is the service's. cur is the task's wake, zero for none: schedule replaces it, keeping where
// it returns the task, wake returns the task there and drops it, and any other verb that moves the task drops it.
func (s *Service) transition(e task.Task, req Request, cur wake.Wake, now time.Time) (task.Task, wakeChange, error) {
	if err := s.accepts(e, req.Verb); err != nil {
		return task.Task{}, wakeChange{}, err
	}
	_, to, _ := s.d.States.Move(string(req.Verb))
	var wc wakeChange
	switch s.d.States.Handler(string(req.Verb)) {
	case handlerSeen:
		e.Changed, e.SeenAt = false, now
	case handlerFocus:
		if err := s.toggleStar(&e, req, now); err != nil {
			return task.Task{}, wakeChange{}, err
		}
	case handlerContext:
		e.SetRole(s.d.States, task.RoleContexts, task.Value{List: split(req.Args["set"])}, task.Writer(req.Actor), now)
	case handlerArea:
		e.SetRole(s.d.States, task.RoleArea, task.Value{Text: strings.TrimSpace(req.Args["set"])}, task.Writer(req.Actor), now)
	case handlerSchedule:
		w, err := scheduleArgs(e, cur, req.Args, now)
		if err != nil {
			return task.Task{}, wakeChange{}, err
		}
		wc.put = &w
	case handlerWaiting:
		if err := waitingArgs(&e, req, now); err != nil {
			return task.Task{}, wakeChange{}, err
		}
	case handlerPromise:
		if name, _ := s.d.States.PropOf(task.RolePromise); name == "" {
			return task.Task{}, wakeChange{}, fmt.Errorf("%w: %s, and no property of tasks.toml takes the promise role", ErrVerb, req.Verb)
		}
		by, err := promiseArg(req.Args["by"], now)
		if err != nil {
			return task.Task{}, wakeChange{}, err
		}
		e.SetRole(s.d.States, task.RolePromise, task.Value{Time: by}, task.Writer(req.Actor), now)
	case handlerWake:
		if err := returnFrom(&e, cur, s.d.States); err != nil {
			return task.Task{}, wakeChange{}, err
		}
		wc.drop = true
	}
	if to != "" {
		if wc.put == nil {
			wc.drop = true
		}
		e.Become(task.State(to), s.d.States)
		e.Changed, e.SeenAt = false, now
	}
	e.Asleep = wc.put != nil || e.Asleep && !wc.drop
	e.Updated = now
	return e, wc, nil
}

// returnFrom moves deferred task e back to the state its wake w returns it to, the first ready state when w
// names none; a task not deferred stays where it is.
func returnFrom(e *task.Task, w wake.Wake, st States) error {
	if e.Category != task.CategoryDeferred {
		return nil
	}
	if w.Return == "" {
		return e.MoveTo(task.CategoryReady, st)
	}
	e.Become(task.State(w.Return), st)
	return nil
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

// promiseArg is the time by names: HH:MM today, or tomorrow once that time passed; a deadline, as
// task.ParseDeadline reads one; zero when by is empty.
func promiseArg(by string, now time.Time) (time.Time, error) {
	by = strings.TrimSpace(by)
	if at, err := time.ParseInLocation("15:04", by, now.Location()); err == nil {
		t := time.Date(now.Year(), now.Month(), now.Day(), at.Hour(), at.Minute(), 0, 0, now.Location())
		if !t.After(now) {
			t = t.AddDate(0, 0, 1)
		}
		return t, nil
	}
	t, err := parseDate("by", by, now)
	if err != nil {
		return time.Time{}, fmt.Errorf("%w; or HH:MM", err)
	}
	return t, nil
}

// scheduleArgs is the wake schedule puts on e, which holds cur: until a date, on activity, or both. It returns
// the task where it was before it was first scheduled.
func scheduleArgs(e task.Task, cur wake.Wake, args map[string]string, now time.Time) (wake.Wake, error) {
	until, err := parseDate("until", args["until"], now)
	if err != nil {
		return wake.Wake{}, err
	}
	onActivity := args["on_activity"] == "true"
	if until.IsZero() && !onActivity {
		return wake.Wake{}, fmt.Errorf("%w: schedule needs until or on_activity", ErrArgs)
	}
	w := wake.Wake{Task: e.ID, Until: until, Return: string(e.State)}
	if e.Snoozed() && cur.Task == e.ID {
		w.Return = cur.Return
	}
	if onActivity {
		w.On = []string{wake.OnActivity}
	}
	return w, nil
}

// waitingArgs sets the owner e waits on, on's first person, and when to check on it, resurface; either left
// out keeps what e holds.
func waitingArgs(e *task.Task, req Request, now time.Time) error {
	if on := split(req.Args["on"]); len(on) > 0 {
		e.SetProp(task.PropOwner, task.Value{Actors: []items.Person{{Key: on[0], Name: on[0]}}}, task.Writer(req.Actor), now)
	}
	resurface, err := parseDate("resurface", req.Args["resurface"], now)
	if err != nil {
		return err
	}
	if !resurface.IsZero() {
		e.SetProp(task.PropCheckOn, task.Value{Time: resurface}, task.Writer(req.Actor), now)
	}
	return nil
}
