package task

import "time"

// activeMute is f's mute unless its date has passed.
func activeMute(f UnitFacts, now time.Time) *Mute {
	if f.Mute == nil || (!f.Mute.Until.IsZero() && !f.Mute.Until.After(now)) {
		return nil
	}
	return f.Mute
}

// applyMute snoozes a live entry under m once per mute: an entry woken by activity keeps the mark and stays
// where the user put it, and an entry the user scheduled keeps the user's snooze. With m nil it returns a
// muted entry to its state.
func applyMute(e Task, m *Mute, st States) Task {
	switch {
	case m != nil && e.Open() && !e.Snooze.Mute && !e.Snoozed():
		ret := e.State
		e.MoveTo(CategoryDeferred, st)
		e.Snooze = Snooze{Until: m.Until, OnActivity: m.UntilStateChange, Return: ret, Mute: true}
	case m != nil && e.Snoozed() && e.Snooze.Mute:
		e.Snooze.Until, e.Snooze.OnActivity = m.Until, m.UntilStateChange
	case m == nil && e.Snooze.Mute:
		if e.Snoozed() {
			e.returnFrom(e.Snooze, st)
		}
		e.Snooze = Snooze{}
	}
	return e
}

// returnFrom moves the task back to the state s returns it to, the Inbox when s names none.
func (e *Task) returnFrom(s Snooze, st States) {
	if s.Return == "" {
		e.MoveTo(CategoryInbox, st)
		return
	}
	e.Become(s.Return, st)
}

// Wake returns a snoozed task to Snooze.Return when Until <= now.
func Wake(e Task, st States, now time.Time) (Task, bool) {
	if !e.Snoozed() || e.Snooze.Until.IsZero() || e.Snooze.Until.After(now) {
		return e, false
	}
	e.returnFrom(e.Snooze, st)
	e.Snooze = Snooze{}
	e.Updated = now
	return e, true
}

// Activity returns snoozed and waiting entries of f.Unit to Inbox with Changed set when f.LastHuman is after
// what the user saw (SeenAt, or Created before any), unless the unit is muted without UntilStateChange.
func Activity(f UnitFacts, have []Task, st States, now time.Time) []Change {
	if m := activeMute(f, now); m != nil && !m.UntilStateChange {
		return nil
	}
	var out []Change
	for _, e := range have {
		if !e.Snoozed() && e.Category != CategoryWaiting {
			continue
		}
		if !f.LastHuman.After(later(e.SeenAt, e.Created)) {
			continue
		}
		e.MoveTo(CategoryInbox, st)
		e.Changed, e.Updated = true, now
		e.Snooze = Snooze{Mute: e.Snooze.Mute}
		out = append(out, Change{Op: OpReinbox, Task: e, Why: "activity on " + f.Unit.String()})
	}
	return out
}

func later(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}
