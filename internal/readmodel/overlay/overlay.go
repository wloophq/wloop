// Package overlay lays the host's own state over a unit's situation as its
// source derives it: the loop's run on the unit, the user's mute and a
// pending round. It names no source: any plugin's row and card take the
// same overlays.
package overlay

import (
	"strings"
	"time"

	"github.com/wloophq/wloop/internal/domain/wake"
	"github.com/wloophq/wloop/internal/intent/builtin"
)

// The situations the overlays set.
const (
	KindDone         = "done"
	KindMuted        = "muted"
	KindRoundPending = "round_pending"
	KindLoopWorking  = "loop_working"
	KindLoopWaiting  = "loop_waiting"
	KindLoopStopped  = "loop_stopped"
)

// WaitsOnMe is the WaitingOn of a situation waiting on the user.
const WaitsOnMe = "me"

// ReasonMax bounds a reason that comes from an agent or a source, so a row
// and a notification stay one line.
const ReasonMax = 200

// Situation is what a unit waits on, since when and why.
type Situation struct {
	Kind      string
	WaitingOn string // logins, "me", "author", "nobody"
	Since     time.Time
	Reason    string
	Stalled   bool
}

// Turn is what the loop's store says of the turn that drives a unit's row:
// its newest turn of an intent that drives the row, else its newest turn.
type Turn struct {
	Intent string
	// Role is the role the turn's intent held.
	Role           string
	State, Summary string
	// At is when the turn reached its state; AnsweredAt is when an answer
	// was typed or resumed after its last record, zero without one.
	At, AnsweredAt time.Time
}

// Situation is the situation t decides while its session waits or not;
// false when its role does not drive the row, or its state leaves the
// situation to the source.
func (t Turn) Situation(sessionWaiting bool) (Situation, bool) {
	if role, ok := builtin.RoleOf(t.Role); !ok || !role.DrivesRow {
		return Situation{}, false
	}
	at := func(k, reason string) (Situation, bool) {
		return Situation{Kind: k, WaitingOn: WaitsOnMe, Since: t.At, Reason: Cut(reason)}, true
	}
	switch t.State {
	case "asked":
		if t.AnsweredAt.After(t.At) {
			return at(KindLoopWorking, "your answer was sent")
		}
		return at(KindLoopWaiting, t.Summary)
	case "failed":
		return at(KindLoopStopped, t.Summary)
	case "unrecorded":
		if t.Summary == "" {
			return at(KindLoopStopped, "the agent ended its turn without a record")
		}
		return at(KindLoopStopped, "the agent ended its turn without a record: "+t.Summary)
	case "started":
		if sessionWaiting {
			return at(KindLoopWaiting, "waiting for a permission in the terminal")
		}
		return at(KindLoopWorking, t.Intent+" round in progress")
	}
	return Situation{}, false
}

// AsksUser says whether an overlay's situation kind asks the user to act:
// the agent asks or stopped, or a round waits for an answer.
func AsksUser(kind string) bool {
	return kind == KindLoopWaiting || kind == KindLoopStopped || kind == KindRoundPending
}

// Mute is the user's mute of a unit, its task's wake: until a time, or
// until the task's facts change, which the wakes match.
type Mute struct {
	Until            time.Time
	UntilStateChange bool
	// At is when the user muted it; zero when unknown.
	At time.Time
}

// MuteOf is the mute wake w holds on a unit's task: until its Until, and
// until a change while it waits on a condition or an event; a spent wake
// mutes nothing.
func MuteOf(w wake.Wake) Mute {
	return Mute{Until: w.Until, UntilStateChange: w.When != "" || len(w.On) > 0}
}

// Set says whether m mutes anything at all, held or lapsed.
func (m Mute) Set() bool { return !m.Until.IsZero() || m.UntilStateChange }

// Holds says whether m mutes a unit whose situation, unmuted, is s.
func (m Mute) Holds(s Situation, now time.Time) bool {
	if s.Kind == KindDone {
		return false
	}
	return m.UntilStateChange || now.Before(m.Until)
}

// Refusal is why m refuses a turn at now: muted until the state changes,
// or until a time; "" when it does not.
func (m Mute) Refusal(now time.Time) string {
	switch {
	case m.UntilStateChange:
		return "muted until its state changes"
	case now.Before(m.Until):
		return "muted until " + m.Until.Local().Format(time.DateTime)
	}
	return ""
}

// Muted is the situation of a unit m mutes.
func (m Mute) Muted() Situation {
	return Situation{Kind: KindMuted, WaitingOn: "nobody", Since: m.At, Reason: "muted"}
}

// Pending is an automatic round waiting for the user's answer.
type Pending struct {
	Intent, Reason  string
	Since, Deadline time.Time
}

// Over is s with p pending on it: the round waits on the user unless the
// unit is done or muted.
func (p Pending) Over(s Situation) Situation {
	if s.Kind == KindDone || s.Kind == KindMuted {
		return s
	}
	return Situation{Kind: KindRoundPending, WaitingOn: WaitsOnMe, Since: s.Since, Reason: p.Intent + " waiting for your answer: " + p.Reason}
}

// Cut keeps s within ReasonMax runes.
func Cut(s string) string {
	if r := []rune(s); len(r) > ReasonMax {
		return strings.TrimRight(string(r[:ReasonMax-1]), " ") + "…"
	}
	return s
}
