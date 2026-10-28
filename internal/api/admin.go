package api

import (
	"time"

	"github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/workflow"
)

// ReconcileReport is what the daemon found and repaired when it started,
// before it served UserAPI.
type ReconcileReport struct {
	// Unrecorded are the pull requests whose turn ended without a record.
	Unrecorded []unit.Unit `json:"unrecorded"`
	// Closed are the pull requests whose asked turn my review closed.
	Closed       []unit.Unit `json:"closed"`
	GoneSessions []string    `json:"gone_sessions"`
	// Rewritten counts the view items the sinks were brought back to.
	Rewritten int       `json:"rewritten"`
	At        time.Time `json:"at"`
}

// HomeFinding is one entry an agent home lacks or holds wrong; Fix is the
// command that repairs it, "" when none does.
type HomeFinding struct {
	Home    string `json:"home"`
	Entry   string `json:"entry"`
	Problem string `json:"problem"`
	Fix     string `json:"fix,omitempty"`
}

// Warning is the finding as a line of Health.Warnings.
func (f HomeFinding) Warning() string {
	w := "agent home " + f.Home + ": " + f.Entry + ": " + f.Problem
	if f.Fix != "" {
		w += "; fix: " + f.Fix
	}
	return w
}

// DryRunTurnRequest asks for the review turn of a `profile try`.
type DryRunTurnRequest struct {
	PR  string        `json:"pr"`
	Why *workflow.Why `json:"why,omitempty"`
}

// DryRunTurn is the stored dry-run turn the command line runs the agent on.
type DryRunTurn struct {
	ID     string `json:"id"`
	Prompt string `json:"prompt"`
	// Why is the why section the instructions rendered; empty without one.
	Why string `json:"why,omitempty"`
}

// Doctor marks.
const (
	DoctorOK      = "ok"
	DoctorMissing = "missing"
	DoctorInfo    = "info"
)

// DoctorCheck is one line of `doctor`; Fix is printed for a missing check.
type DoctorCheck struct {
	Mark string `json:"mark"`
	Name string `json:"name"`
	Info string `json:"info,omitempty"`
	Fix  string `json:"fix,omitempty"`
}

// DoctorQuery narrows the state `profile show` reads to one repository
// or, with PR, one pull request.
type DoctorQuery struct {
	Repo string `json:"repo,omitempty"`
	PR   string `json:"pr,omitempty"`
}

// DoctorReport is doctor's checks that read the loop's state.
type DoctorReport struct {
	Checks []DoctorCheck `json:"checks"`
	// BundledChanged are the intents whose bundled guide changed since the
	// queried repository's last round.
	BundledChanged []string `json:"bundled_changed,omitempty"`
	// Reconcile is the start-up reconcile's report; nil while it runs.
	Reconcile *ReconcileReport `json:"reconcile,omitempty"`
	// Workflows are the checks of the workflows the daemon runs.
	Workflows []DoctorCheck `json:"workflows,omitempty"`
}

// TurnBook is a pull request's book as `turn show` prints it.
type TurnBook struct {
	Unit  Named       `json:"unit"`
	Turns []TurnShown `json:"turns"`
}

// TurnShown is one turn with its stored instructions and its events: the
// records and the answers it got.
type TurnShown struct {
	Turn         turn.Turn     `json:"turn"`
	Instructions string        `json:"instructions,omitempty"`
	Records      []turn.Record `json:"records"`
	Answers      []turn.Answer `json:"answers"`
}

// MeterScope is the cost of one scope since the daemon started. For a job or
// a loop that has phases, Stats holds the scope and its phases together and
// each phase is listed under its own "<scope>/<phase>" name with its own
// samples only.
type MeterScope struct {
	Name     string      `json:"name"`
	Runs     int64       `json:"runs"`
	WallMS   int64       `json:"wall_ms"`
	Stats    []MeterStat `json:"stats"`
	Last     []MeterStat `json:"last,omitempty"`
	LastAt   time.Time   `json:"last_at,omitzero"`
	LastWall int64       `json:"last_wall_ms,omitempty"`
}

// MeterReport is every scope's metered cost since the daemon started, sorted
// by name, and the same for the last minutes in Recent.
type MeterReport struct {
	Since  time.Time    `json:"since"`
	At     time.Time    `json:"at"`
	Load   Load         `json:"load"`
	Scopes []MeterScope `json:"scopes"`
	Recent *MeterWindow `json:"recent,omitempty"`
}

// MeterWindow is what each scope spent since Since, a recent minute, with
// MeterScope's roll-up of phases; Last and its fields are left out.
type MeterWindow struct {
	Since  time.Time    `json:"since"`
	Scopes []MeterScope `json:"scopes"`
}
