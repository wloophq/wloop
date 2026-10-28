package turns

import (
	"context"
	"errors"
	"time"

	"github.com/wloophq/wloop/internal/domain/task"

	coreitems "github.com/wloophq/wloop/internal/core/items"
	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/domain/gate"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	"github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
)

// The kinds of the events the turns' saves write.
const (
	KindTurnSent            = "turn_sent"          // Key = turn id
	KindTurnDelivered       = "turn_delivered"     // Key = turn id:handle
	KindTurnWithdrawn       = "turn_withdrawn"     // Key = turn id:withdrawn-at, Data = why
	KindTurnStarted         = "turn_started"       // Key = turn id
	KindTurnEnded           = "turn_ended"         // Key = turn id:hash[:12]
	KindAnswered            = "answered"           // Data = the answer on one line
	KindAnswerRouted        = "answer_routed"      // Key = answer id, Data = the route and the handle
	KindQuestionDismissed   = "question_dismissed" // Key = turn id
	KindTurnResumed         = "turn_resumed"       // Key = turn id
	KindSessionStarted      = "session_started"    // Key = handle:started-at
	KindSessionLaunched     = "session_launched"   // Key = handle
	KindSessionAbandoned    = "session_abandoned"  // Key = handle
	KindMCPSeen             = "mcp_seen"           // Key = handle:mcp-at
	KindTurnStopped         = "turn_stopped"       // Key = handle:prompt_id
	KindStopBlocked         = "stop_blocked"       // Key = handle:prompt_id
	KindSessionIdle         = "session_idle"       // Key = handle:idle-since
	KindSessionActive       = "session_active"     // Key = handle:idle-since
	KindSessionClosed       = "session_closed"     // Key = handle, Data = the reason, then "; resume with: " and the line
	KindReconcileReport     = "reconcile_report"
	KindSessionTyped        = "session_typed"    // Key = handle:typed-at
	KindSessionPrompted     = "session_prompted" // Key = handle:seen-at, Data = "user" or "loop"
	KindTurnDismissedByRule = "turn_dismissed"   // Key = seq:reason, Data = the reason
	KindAnswerRefused       = "answer_refused"   // Key = answer id, Data = the reason
	KindAnswerRead          = "answer_read"      // Key = answer id:read-at
)

// ErrWorkspaceGone is what a Launcher returns when the workspace a resume
// runs in no longer exists.
var ErrWorkspaceGone = errors.New("the workspace is gone")

// Key finds a book by the id of one of its turns or by the handle a turn
// was sent to or started on; the newest matching turn wins.
type Key struct{ Turn, Session string }

type Store interface {
	// Books has a book for every unit, empty when it has no turn; with no unit,
	// the book of every unit with a turn.
	Books(ctx context.Context, units ...unit.Unit) (map[unit.Unit]turn.Book, error)
	BookOf(ctx context.Context, k Key) (turn.Book, bool, error)
	// UnitsOf maps each handle to the unit of the newest turn sent to or
	// started on it; a handle with no turn is absent.
	UnitsOf(ctx context.Context, handles []string) (map[string]unit.Unit, error)
	// Save writes the book and evs in one transaction and returns the book as
	// stored, with Seq and ID assigned to its new entries. A new record or
	// answer with TurnSeq 0, and a replaced turn with Before set and
	// ReplacedBy 0, belong to the book's newest new turn.
	Save(ctx context.Context, b turn.Book, evs ...port.Event) (turn.Book, error)
	// Open lists the units with a sent, started or asked turn.
	Open(ctx context.Context) ([]unit.Unit, error)
}

type Reports interface {
	// WithTurns lists the units with at least one turn of i that is not
	// withdrawn.
	WithTurns(ctx context.Context, i turn.Intent) ([]unit.Unit, error)
}

// Results attaches to an entry the unit a turn on it produced.
type Results interface {
	// UnitOf is the unit a web address names; false for an address of none.
	UnitOf(url string) (unit.Unit, bool)
	Attach(ctx context.Context, tk task.ID, u unit.Unit) error
}

// Intents is what the turns ask of the loaded intents: the record rules, the
// intents an answer can address and what closes, reconciles and titles
// their sessions.
type Intents interface {
	// Rules errs, saying why, for an intent whose records cannot be checked.
	Rules(intent string) (dintent.RecordRules, error)
	Answerable() []string
	ClosesWhenDone(intent string) bool
	Reconcile(intent string) string
	SessionTitles() []string
}

// Gates opens the gates of gates.toml by name.
type Gates interface {
	// OpenNamed opens the gate name declares, of its kind and intents, on g's
	// task and unit.
	OpenNamed(ctx context.Context, name string, g gate.Gate) (gate.Gate, error)
}

// StopsKept is how long the store keeps a stop.
const StopsKept = 30 * 24 * time.Hour

// LastStop returns the newest stop of one handle.
func LastStop(ctx context.Context, r Registry, handle string) (turn.Stop, bool, error) {
	got, err := r.LastStops(ctx, []string{handle})
	st, ok := got[handle]
	return st, ok, err
}

// Registry is the sessions; the store behind it also keeps their Handles.
type Registry interface {
	Session(ctx context.Context, id string) (turn.Session, bool, error)
	SaveSession(ctx context.Context, s turn.Session, evs ...port.Event) error
	// Sessions lists the sessions not closed.
	Sessions(ctx context.Context) ([]turn.Session, error)
	// LastStops returns the newest stop of each handle that logged one.
	LastStops(ctx context.Context, handles []string) (map[string]turn.Stop, error)
	// LogStop stores s.
	LogStop(ctx context.Context, s turn.Stop, evs ...port.Event) error
}

// Prompts reads, from the agent's own transcript, when a person last
// prompted a session.
type Prompts interface {
	// LastUserPrompt is ok false when the session's transcript cannot be
	// found or read; a readable one with no prompt is ok with a zero at.
	LastUserPrompt(ctx context.Context, sess turn.Session) (at time.Time, ok bool, err error)
}

// AgentSessions keeps what the hooks tell of each agent session, so a turn's
// usage is found after its session moved on.
type AgentSessions interface {
	SaveAgentSession(ctx context.Context, a AgentSessionInfo) error
}

type AgentSessionInfo struct {
	ID, Kind, Session, Home, Worktree, Transcript string
	At                                            time.Time
}

// SourceAnswer is the user's answer, in a unit's source, to a turn that
// asked them, such as their review of a pull request.
type SourceAnswer struct {
	// State is the answer's state in its source; Verdict is the record's
	// verdict it gives, "" for a state that answers nothing.
	ID, State, Verdict, URL string
	At                      time.Time
}

// Answers reads the user's answers on a unit since a time.
type Answers interface {
	Answers(ctx context.Context, u unit.Unit, since time.Time) ([]SourceAnswer, error)
}

// Ledger is the items ledger a record's ops go to, and the unit's items and
// op outcomes its turns' instructions list.
type Ledger interface {
	coreitems.Applier
	Log(ctx context.Context, u unit.Unit, since time.Time) ([]coreitems.LogEntry, error)
}
