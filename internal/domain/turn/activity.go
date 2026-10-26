package turn

import "time"

// Activity is what the loop spent on a unit: its turns, the newest one's
// intent, their tokens and cost, and when its brief retries.
type Activity struct {
	Turns, OpenTurns int
	LastIntent       string
	Tokens           int64
	CostUSD          float64
	BriefRetryAt     time.Time
}
