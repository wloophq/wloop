package api

import "time"

// Intake paths. Intakes answers ?after=<n>&wait=<seconds>: the log lines
// past the n-th, once there are any or the intake ends, waiting at most
// IntakeWaitMax seconds.
const (
	Intakes       = "/api/intakes/{seq}"
	IntakeWithout = "/api/intakes/{seq}/without"
	IntakeCancel  = "/api/intakes/{seq}/cancel"
)

const IntakeWaitMax = 25

// The states of an intake.
const (
	IntakeAccepted = "accepted"
	IntakeBriefing = "briefing"
	IntakeStarting = "starting"
	IntakeStarted  = "started"
	IntakeFailed   = "failed"
	IntakeCanceled = "canceled"
)

// Intake is a turn a user asked for, waiting for its pull request's brief
// or started. Log holds the lines past the asked index and LogLen counts
// every line; Attempt and LastError are the brief attempt it waits on and
// the failure of the one before.
type Intake struct {
	Seq       int64     `json:"seq"`
	PR        string    `json:"pr"`
	Intent    string    `json:"intent"`
	Source    string    `json:"source"`
	State     string    `json:"state"`
	Brief     string    `json:"brief,omitempty"`
	Note      string    `json:"note,omitempty"`
	Attempt   int       `json:"attempt,omitempty"`
	LastError string    `json:"last_error,omitempty"`
	Next      time.Time `json:"next,omitzero"`
	Error     string    `json:"error,omitempty"`
	Joined    bool      `json:"joined,omitempty"`
	Log       []string  `json:"log,omitempty"`
	LogLen    int       `json:"log_len"`
}

// Waiting tells an intake that waits for the brief.
func (i Intake) Waiting() bool { return i.State == IntakeAccepted || i.State == IntakeBriefing }

// Open tells an intake that has not ended.
func (i Intake) Open() bool { return i.Waiting() || i.State == IntakeStarting }

// BriefsHealth is the brief breaker: Open while briefs are unavailable,
// since Since for Error, with the next trial at Next. Unavailable says why
// the daemon runs no brief at all while brief.enabled is set.
type BriefsHealth struct {
	Unavailable string    `json:"unavailable,omitempty"`
	Open        bool      `json:"open"`
	Since       time.Time `json:"since,omitzero"`
	Error       string    `json:"error,omitempty"`
	Next        time.Time `json:"next,omitzero"`
}
