package api

import "time"

// TurnsLivePath serves the turns the floating widget shows.
const TurnsLivePath = "/api/turns/live"

// LiveTurn is one turn as the widget lists it; DurationMS is the time from its start (its send, when it has not
// started) to its end, 0 while it runs.
type LiveTurn struct {
	Seq        int64     `json:"seq"`
	Intent     string    `json:"intent"`
	Task       string    `json:"task,omitempty"`
	Title      string    `json:"title,omitempty"`
	Unit       string    `json:"unit"`
	Started    time.Time `json:"started,omitzero"`
	Ended      time.Time `json:"ended,omitzero"`
	DurationMS int64     `json:"duration_ms,omitempty"`
}

// LiveTurns are the running turns, the turns that ended since the user's local midnight (newest first, at most
// LiveDoneMax), the number of turns asking the user, and the usual duration in milliseconds of each intent's turns
// over the last LiveUsualDays days.
type LiveTurns struct {
	Running []LiveTurn       `json:"running"`
	Done    []LiveTurn       `json:"done"`
	Asked   int              `json:"asked"`
	Usual   map[string]int64 `json:"usual"`
}

const (
	LiveDoneMax   = 20
	LiveUsualDays = 14
)
