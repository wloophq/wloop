package task

import "time"

// ReviewRun is one review: when it started, the steps done (the views they walk), and when the last of them was.
type ReviewRun struct {
	ID         string
	StartedAt  time.Time
	FinishedAt time.Time // zero while a step is open
	Done       []string
}
