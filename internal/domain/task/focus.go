package task

import "time"

const (
	FocusDeadline = "deadline"
	FocusStale    = "stale"
)

// AutoFocus stars an open task whose due date <= now, or a waiting task whose Since is `stale` working days
// (Mon-Fri, in now's location) old with no LastNudge after Since; it clears only stars it set, which a workflow
// wrote. A star the user cleared after its trigger is not set again. It returns the task with its focus
// property set or cleared, the reason ("" when it cleared it), and whether it changed.
func AutoFocus(e Task, stale int, now time.Time) (Task, string, bool) {
	on, w := e.Starred(), e.Written[PropFocus]
	if on && w.By != WriterWorkflow {
		return e, "", false
	}
	reason, at := autoReason(e, stale, now)
	switch {
	case reason == "" && on:
		e.SetProp(PropFocus, Value{}, WriterWorkflow, now)
		return e, "", true
	case reason == "" || on:
		return e, "", false
	case !w.At.IsZero() && !w.At.Before(at):
		return e, "", false
	}
	e.SetProp(PropFocus, Value{Text: FocusOn}, WriterWorkflow, now)
	return e, reason, true
}

func autoReason(e Task, stale int, now time.Time) (string, time.Time) {
	if !e.Open() {
		return "", time.Time{}
	}
	if due := e.Due(); !due.IsZero() && !due.After(now) {
		return FocusDeadline, due
	}
	if w := e.Waiting; w != nil && e.Category == CategoryWaiting && !w.Since.IsZero() && stale > 0 && !w.LastNudge.After(w.Since) {
		if at := AddWorkdays(w.Since, stale, now.Location()); !at.After(now) {
			return FocusStale, at
		}
	}
	return "", time.Time{}
}
