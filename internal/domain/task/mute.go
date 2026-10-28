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
func applyMute(e Task, m *Mute) Task {
	switch {
	case m != nil && e.Live() && !e.Snooze.Mute && e.State != Scheduled:
		e.Snooze = Snooze{Until: m.Until, OnActivity: m.UntilStateChange, Return: e.State, Mute: true}
		e.State = Scheduled
	case m != nil && e.State == Scheduled && e.Snooze.Mute:
		e.Snooze.Until, e.Snooze.OnActivity = m.Until, m.UntilStateChange
	case m == nil && e.Snooze.Mute:
		if e.State == Scheduled {
			e.State = returnState(e.Snooze)
		}
		e.Snooze = Snooze{}
	}
	return e
}

func returnState(s Snooze) State {
	if s.Return == "" || s.Return == Scheduled {
		return Inbox
	}
	return s.Return
}

// Wake returns a Scheduled entry to Snooze.Return when Until <= now.
func Wake(e Task, now time.Time) (Task, bool) {
	if e.State != Scheduled || e.Snooze.Until.IsZero() || e.Snooze.Until.After(now) {
		return e, false
	}
	e.State = returnState(e.Snooze)
	e.Snooze = Snooze{}
	e.Updated = now
	return e, true
}

// Activity returns snoozed and waiting entries of f.Unit to Inbox with Changed set when f.LastHuman is after
// what the user saw (SeenAt, or Created before any), unless the unit is muted without UntilStateChange.
func Activity(f UnitFacts, have []Task, now time.Time) []Change {
	if m := activeMute(f, now); m != nil && !m.UntilStateChange {
		return nil
	}
	var out []Change
	for _, e := range have {
		if e.State != Scheduled && e.State != Waiting {
			continue
		}
		if !f.LastHuman.After(later(e.SeenAt, e.Created)) {
			continue
		}
		e.State, e.Changed, e.Updated = Inbox, true, now
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
