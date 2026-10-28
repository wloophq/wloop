// Package intake runs a turn a user asks for: the request is accepted with a
// number at once, waits for the first turn's brief, then starts the turn.
package intake

import (
	"context"
	"io"
	"time"

	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
)

type State string

const (
	Accepted State = "accepted"
	Briefing State = "briefing"
	Starting State = "starting"
	Started  State = "started"
	Failed   State = "failed"
	Canceled State = "canceled"
)

// Open tells a state that holds its pull request and intent: a request for
// them joins the intake.
func (s State) Open() bool { return s == Accepted || s == Briefing || s == Starting }

func (s State) waiting() bool { return s == Accepted || s == Briefing }

// The values of Intake.Brief.
const (
	BriefReady   = "ready"
	BriefWithout = "without"
	BriefOff     = "off"
)

// The kinds of the events an intake's saves write, keyed by its seq, and
// intake_guidance by <seq>:<n> for its n-th replacement; Data is the note or
// the error.
const (
	KindAccepted = "intake_accepted"
	KindBriefing = "intake_briefing"
	KindStarting = "intake_starting"
	KindStarted  = "intake_started"
	KindFailed   = "intake_failed"
	KindCanceled = "intake_canceled"
	KindGuidance = "intake_guidance"
	// KindLog is a notice published for each log line and never stored.
	KindLog = "intake_log"
)

const (
	NoteGuidance = "guidance replaced by a later request"
	NoteWithout  = "you started it without the brief"
	NoteNoBrief  = "asked to start without the brief"
	NoteStopped  = "the daemon stopped while starting the turn; ask again"
	NoteCanceled = "canceled by the user"
)

// BriefState is what an intake shows of its unit's brief: whether one is
// recorded, the attempts that failed since its last success, whether the
// queue gave up on it, the error of a last run that failed, and when it
// tries again.
type BriefState struct {
	Found     bool
	Failures  int
	GaveUp    bool
	LastError string
	RetryAt   time.Time
}

type Intake struct {
	Seq                                         int64
	Unit                                        unit.Unit
	Intent                                      turn.Intent
	Source                                      string
	Request                                     []byte
	State                                       State
	Brief, Note                                 string
	AcceptedAt, BriefingAt, StartingAt, EndedAt time.Time
	TurnSeq                                     int64
	TurnID, Error                               string
	Log                                         []string
}

type Request struct {
	Unit     unit.Unit
	Intent   turn.Intent
	Source   string
	Payload  []byte
	Guidance string
	NoBrief  bool
	// CanJoin refuses, with its error, to join the open intake of the
	// request's pull request and intent; nil joins any.
	CanJoin func(open Intake) error
}

// The causes of a Readiness that goes without the brief.
const (
	CauseGaveUp  = "gave_up"
	CauseBreaker = "breaker"
	CauseOff     = "off"
)

// The waits of a Readiness.
const (
	WaitBriefing = "briefing"
	WaitRetry    = "retry"
)

// Readiness is the brief queue's answer for a pull request: Go starts the
// turn, with the brief unless Without, and Cause says why without;
// otherwise the intake waits, Wait and Attempt saying on what, until the
// brief changes or Next. LastError is the failure of attempt Attempt-1;
// for CauseGaveUp, Attempt counts the failed attempts and LastError is the
// last one's; for CauseBreaker, Since and LastError say since when and
// why briefs are unavailable.
type Readiness struct {
	Go, Without     bool
	Cause           string
	Wait            string
	Attempt         int
	Note, LastError string
	Next, Since     time.Time
}

// StartedTurn is the turn a Starter sent.
type StartedTurn struct {
	TurnSeq int64
	TurnID  string
}

type Store interface {
	// Accept stores i under a new Seq and writes evs keyed by it, in one
	// transaction. When i's pull request and intent have an open intake, it
	// stores nothing and answers that intake, joined.
	Accept(ctx context.Context, i Intake, evs ...port.Event) (Intake, bool, error)
	Save(ctx context.Context, i Intake, evs ...port.Event) error
	Intake(ctx context.Context, seq int64) (Intake, bool, error)
	// Open lists the open intakes, oldest first.
	Open(ctx context.Context) ([]Intake, error)
}

type Briefs interface {
	// Ready answers for a turn of u; need are the brief intent's extra
	// fields the turn's intent needs, which a first turn waits for.
	Ready(ctx context.Context, u unit.Unit, first bool, need []string) (Readiness, error)
	// Changed is closed by the next end of a brief run of the unit after the
	// call, so a run that ends while the caller asks Ready still wakes it;
	// the func releases it.
	Changed(u unit.Unit) (<-chan struct{}, func())
	// Unwant drops the want of a brief once no intake waits on it.
	Unwant(ctx context.Context, u unit.Unit) error
}

type Firsts interface {
	First(ctx context.Context, u unit.Unit) (bool, error)
}

type Starter interface {
	Start(ctx context.Context, i Intake, out io.Writer) (StartedTurn, error)
}

// AheadStarter is a Starter that reads, before the service takes Lock, what
// its start would read under it; Start gets the returned context.
type AheadStarter interface {
	ReadAhead(ctx context.Context, i Intake) context.Context
}

// Payloads rewrites a stored request's guidance, for a joining request.
type Payloads interface {
	WithGuidance(payload []byte, guidance string) ([]byte, error)
}
