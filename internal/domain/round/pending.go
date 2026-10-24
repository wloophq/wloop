// Package round holds the values of the loop's automatic rounds.
package round

import "time"

// Pending is an automatic round that waits for the user's answer until
// Deadline, then runs.
type Pending struct {
	Intent   string
	Reason   string
	Since    time.Time
	Deadline time.Time
}
