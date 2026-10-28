package task

import "time"

const (
	FocusDeadline = "deadline"
	FocusStale    = "stale"
)

// AutoFocus stars an entry whose Deadline <= now, or a waiting entry whose Since is `stale` working days
// (Mon-Fri, in now's location) old with no LastNudge after Since; it clears only stars it set. A star the
// user cleared after its trigger is not set again.
func AutoFocus(e Task, stale int, now time.Time) (Focus, bool) {
	if e.Focus.On && e.Focus.Auto == "" {
		return e.Focus, false
	}
	reason, at := autoReason(e, stale, now)
	switch {
	case reason == "" && e.Focus.On:
		return Focus{}, true
	case reason == "" || e.Focus.On:
		return e.Focus, false
	case !e.Focus.At.IsZero() && !e.Focus.At.Before(at):
		return e.Focus, false
	}
	return Focus{On: true, Auto: reason, At: now}, true
}

func autoReason(e Task, stale int, now time.Time) (string, time.Time) {
	if !e.Live() {
		return "", time.Time{}
	}
	if !e.Deadline.IsZero() && !e.Deadline.After(now) {
		return FocusDeadline, e.Deadline
	}
	if w := e.Waiting; w != nil && e.State == Waiting && !w.Since.IsZero() && stale > 0 && !w.LastNudge.After(w.Since) {
		if at := AddWorkdays(w.Since, stale, now.Location()); !at.After(now) {
			return FocusStale, at
		}
	}
	return "", time.Time{}
}
