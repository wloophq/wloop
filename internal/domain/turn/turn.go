// Package turn is the book of a pull request's turns: what the loop sent, what
// the agent recorded and what the user answered.
package turn

import (
	"errors"
	"time"

	"github.com/wloophq/wloop/internal/domain/record"
	"github.com/wloophq/wloop/internal/domain/unit"
)

// ErrNoQuestion is returned when the pull request has no open question.
var ErrNoQuestion = errors.New("no open question")

type Intent string

type Trigger string

const (
	ByHand    Trigger = "by-hand"
	Automated Trigger = "automated"
)

type State string

const (
	Sent       State = "sent"
	Started    State = "started"
	Asked      State = "asked"
	Done       State = "done"
	Settled    State = "settled"
	Failed     State = "failed"
	Unrecorded State = "unrecorded"
	Dismissed  State = "dismissed"
	Replaced   State = "replaced"
	// Withdrawn is a sent turn the loop failed to type: the send is undone and
	// the book reads as if it never happened.
	Withdrawn State = "withdrawn"
)

func (s State) Open() bool    { return s == Sent || s == Started || s == Asked }
func (s State) Stopped() bool { return s == Failed || s == Unrecorded }

type Turn struct {
	Seq                             int64
	Intent                          Intent
	ID                              string
	Trigger                         Trigger
	Guidance, Head, SentTo, Session string
	State                           State
	SentAt, StartedAt, EndedAt      time.Time
	ResumeOf                        int64
	// ReplacedBy and Before are, for a turn a send replaced, the seq of the
	// turn sent and the state it had; ReplacedBy is 0 until that turn is
	// stored.
	ReplacedBy int64
	Before     State
	// Agent is the agent of the turn's project at send, BriefRun the
	// brief run its instructions carried (0 for none) and AgentSession the
	// agent session that started it.
	Agent        string
	BriefRun     int64
	AgentSession string
	// Inputs are the intent's inputs the turn was sent with.
	Inputs map[string]any
	// Runner is empty for a session turn.
	Runner Runner `json:",omitempty"`
	// Workflow is the workflow whose turn.start step started it, "" none.
	Workflow string `json:",omitempty"`
	// Role is the role its intent held when it was sent, so the history
	// reads by role whatever the intent is named; "" none.
	Role string `json:",omitempty"`
}

type Runner string

const (
	RunnerSession  Runner = "session"
	RunnerHeadless Runner = "headless"
)

type Record struct {
	ID, TurnSeq                       int64
	Session, AgentSession, Head, Hash string
	At                                time.Time
	Acked                             bool
	Record                            record.Record
}

type Route string

const (
	Pending Route = "pending"
	// Queued is an answer that waits for its busy session to stop.
	Queued Route = "queued"
	Typed  Route = "typed"
	// Hooked is an answer whose notice blocked its session's stop.
	Hooked  Route = "hooked"
	Resumed Route = "resumed"
	Refused Route = "refused"
	Direct  Route = "direct"
)

// Noticed says that the answer's session was told of it: typed into it, or noticed through its Stop hook.
func (r Route) Noticed() bool { return r == Typed || r == Hooked }

type Source string

const (
	Cockpit      Source = "cockpit"
	Notification Source = "notification"
	TUI          Source = "tui"
	Terminal     Source = "terminal"
)

type Answer struct {
	ID, TurnSeq    int64
	Text, OptionID string
	// Yes and No are a confirm's explicit choice; Text is then a note.
	Yes, No bool
	Source  Source
	Route   Route
	Session string
	At      time.Time
	// Message is the Inbox message the answer answers, 0 for one stored before answers named theirs.
	Message int64
	// Posted says that Message is one the agent posted during its turn, not its turn's question; the store
	// reads it from the message.
	Posted bool
	// NoticedAt is when the answer's session was told of it, zero until it is.
	NoticedAt time.Time
	// ReadAt is when the agent read the answer, zero until it does.
	ReadAt time.Time
}

// Book holds one unit's turns in ascending Seq, new ones last.
// Pointers it returns point into its slices and are valid until the next
// append.
type Book struct {
	Unit    unit.Unit
	Turns   []Turn
	Records []Record
	Answers []Answer
	// HistoryArtifact names the artifact kind an intent's history entry
	// carries; nil means none.
	HistoryArtifact func(intent string) string
}
