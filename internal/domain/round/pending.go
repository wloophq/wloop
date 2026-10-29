// Package round holds the values of the loop's automatic rounds.
package round

import (
	"strings"
	"time"
)

// Pending is an automatic round that waits for the user's answer until
// Deadline, then runs.
type Pending struct {
	Intent   string
	Reason   string
	Since    time.Time
	Deadline time.Time
}

// heldPrefix starts the subject of the plan gate of a round the daily cap held.
const heldPrefix = "held:"

// HeldSubject is the subject of the plan gate of a round the daily cap held at at: the local day, the day the
// cap counts in, so the next day's hold is a new decision.
func HeldSubject(at time.Time) string { return heldPrefix + at.Local().Format(time.DateOnly) }

// HeldDay is the local day a held round's gate subject names; false for any other subject.
func HeldDay(subject string) (string, bool) { return strings.CutPrefix(subject, heldPrefix) }
