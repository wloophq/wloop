// Package draft holds the Outbox drafts: posts the loop proposes and the
// user approves before they reach a provider.
package draft

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/wloophq/wloop/internal/domain/unit"
)

type ID int64

// Kind is a draft kind's name, which its plugin's manifest declares.
type Kind string

// Delivery is how a draft reaches the provider, as its plugin's manifest
// declares it.
type Delivery string

// Broadcast says whether a thread reply is also posted to its conversation.
// It is NoBroadcast when the draft replies in no thread, as its publisher
// places it when it is proposed.
type Broadcast string

const (
	NoBroadcast  Broadcast = ""
	BroadcastOff Broadcast = "off"
	BroadcastOn  Broadcast = "on"
)

func (b Broadcast) Valid() bool { return b == NoBroadcast || b == BroadcastOff || b == BroadcastOn }

// Placed is where its publisher puts a draft when it is proposed: the target
// a draft with none posts to, "" for its unit, and its Broadcast.
type Placed struct {
	Target    string
	Broadcast Broadcast
}

// State is where a draft's delivery stands; whether it may be posted at all
// is its gate's decision. A draft is held while its gate is open, queued
// once its gate approved it, and dropped when its gate refused it or its
// proposer withdrew it.
type State string

const (
	Held    State = "held"
	Queued  State = "queued"
	Sending State = "sending"
	Sent    State = "sent"
	Failed  State = "failed"
	Dropped State = "dropped"
)

var States = []State{Held, Queued, Sending, Sent, Failed, Dropped}

// Open are the states in which a draft may still be posted.
var Open = []State{Held, Queued, Sending}

type OriginKind string // "turn", "session", "auto", "user"

const (
	OriginTurn    OriginKind = "turn"
	OriginSession OriginKind = "session"
	OriginAuto    OriginKind = "auto"
	OriginUser    OriginKind = "user"
)

type Origin struct {
	Kind     OriginKind
	TurnSeq  int64  // Kind "turn": the turn (session or headless) that proposed it; Kind "session": the handle's newest turn on the unit, 0 none
	Intent   string // the proposing turn's intent ("" for auto and user)
	Workflow string // the workflow that started the proposing turn (turn.Turn.Workflow), "" none
	Rule     string // Kind "auto": the core or plugin rule, e.g. "stalled-nudge", "closure", "ask-owner", "github-nudge"
	Key      string // Kind "auto": the rule's dedupe key, e.g. "stalled-nudge:<task>:<person key>"
	// Unit is the unit of the proposing turn when it is not the draft's, such
	// as a pull request whose turn drafts in a Slack thread; zero when it is.
	Unit unit.Unit
	// Session is the session that proposed it through its tools, which never
	// resolves its gate; "" when no session did.
	Session string
}

type Evidence struct {
	Ref       string // Slack: message ref, <conversation>/<ts>; GitHub: comment id
	Permalink string
	At        time.Time
}

type Draft struct {
	ID        ID
	Unit      unit.Unit
	Items     []string // item ids of Unit; may be empty
	Task      string   // the task it serves, "" none; the Outbox row of the draft itself is "draft:<ID>"
	Kind      Kind
	Purpose   string    // what it is for: "nudge", "close", "ask_owner", or any label its proposer names
	Delivery  Delivery  // how it is posted
	Effects   []string  // applied when it is sent, such as "task.nudged"
	Target    string    // plugin target grammar; "" means the unit itself (its thread, its PR)
	Broadcast Broadcast // a thread reply also posted to its conversation; NoBroadcast when it replies in no thread
	Text      string    // standard Markdown with mentions as @{<person key>}; the Effects implementation converts both; Kind "delete": the text of the message it removed, as Slack held it
	Reaction  string    // Kind "react" only: emoji name without colons
	Replaced  string    // Kind "edit" only: the text the edit replaced
	Original  string    // Text as proposed
	Edited    bool      // Text != Original
	Origin    Origin
	State     State
	Evidence  Evidence // State "sent" only
	Error     string   // State "failed" only, never a token
	Reason    string   // State "dropped": "user", "assigned", "moot", "dry_run", ...
	// Rev is the draft's content revision: 1 as proposed, and one more for each write that changes its
	// Content. A gate approves a draft at its Rev; a person and an agent revise it at the Rev they read.
	Rev int64
	// Version is the stored row's write counter, which every write bumps and checks, a state move as much as
	// a revision.
	Version int64
	// EffectsAppliedAt is when the Effects of a sent draft were applied;
	// zero while they are not.
	EffectsAppliedAt time.Time
	// Why is why it was proposed, in the user's language when it was;
	// Language is the locale Text is written in. Both "" for a draft no
	// workflow step wrote.
	Why, Language string
	// Payload is what the kind's schema shapes beside Text, posted
	// verbatim; nil for a kind without one.
	Payload json.RawMessage
	// Basis is what the proposer read the unit at, such as a head commit,
	// for a kind whose KindSpec.Basis is BasisAgent; "" none.
	Basis string
	// Hold keeps the draft waiting for a person whatever its approval
	// rules say, except under dry_run.
	Hold bool
	// By is who wrote the current content (Text, Payload, Basis); the outbox sets it on Propose, Amend
	// and AgentAmend.
	By Actor

	CreatedAt, UpdatedAt, SendingAt time.Time
}

// ReasonClosedSilently is the Reason of a draft the user discarded and closed without its message: its
// effects apply as if it was sent.
const ReasonClosedSilently = "closed_silently"

// EffectsDue says whether d's effects are owed: it was sent, or closed without its message.
func (d Draft) EffectsDue() bool {
	return d.State == Sent || d.State == Dropped && d.Reason == ReasonClosedSilently
}

// ErrTransition is wrapped by Can.
var ErrTransition = errors.New("draft: transition not allowed")

// Can says why d cannot move to s, nil when it can:
// held->queued|failed|dropped; queued->sending|failed|dropped;
// sending->sent|failed; failed->queued (by hand only)|dropped.
func (d Draft) Can(s State) error {
	if !slices.Contains(transitions[d.State], s) {
		return fmt.Errorf("%w: %s -> %s", ErrTransition, d.State, s)
	}
	return nil
}

var transitions = map[State][]State{
	Held:    {Queued, Failed, Dropped},
	Queued:  {Sending, Failed, Dropped},
	Sending: {Sent, Failed},
	Failed:  {Queued, Dropped},
}

// The reasons a draft is withdrawn, as Reason of the discarded draft: a
// workflow's, its person was assigned the items or it is moot; a verb
// ended the task the draft served; the user skipped its workflow on its
// unit; or one draft to the whole conversation replaced it. Outside: it addresses someone who cannot read
// the conversation it posts in. Joined: the person it was written to in their own conversation joined the
// one it came from.
const (
	WithdrawAssigned   = "assigned"
	WithdrawMoot       = "moot"
	WithdrawOutside    = "outside"
	WithdrawJoined     = "joined"
	WithdrawEnded      = "ended"
	WithdrawSkipped    = "skipped"
	WithdrawSuperseded = "superseded"
)

// WithdrawReasons are every reason a draft is withdrawn with.
var WithdrawReasons = []string{WithdrawAssigned, WithdrawMoot, WithdrawOutside, WithdrawJoined, WithdrawEnded, WithdrawSkipped, WithdrawSuperseded}

type ApprovalMode string // "wait" (default), "delay", "send", "dry_run"

const (
	Wait   ApprovalMode = "wait"
	Delay  ApprovalMode = "delay"
	Send   ApprovalMode = "send"
	DryRun ApprovalMode = "dry_run"
)

// ApprovalModes are the draft approval's modes.
var ApprovalModes = []ApprovalMode{Send, Delay, Wait, DryRun}

// Releases says whether m sends a draft with no person approving it.
func (m ApprovalMode) Releases() bool { return m == Send || m == Delay }

type Approval struct {
	Mode   ApprovalMode
	Window time.Duration // Mode "delay" only, > 0
}

// Validate refuses an unknown mode, a window <= 0 with delay, a window set without delay.
func (a Approval) Validate() error {
	switch {
	case !slices.Contains(ApprovalModes, a.Mode):
		return fmt.Errorf("unknown draft approval mode %q", a.Mode)
	case a.Mode == Delay && a.Window <= 0:
		return errors.New("draft approval delay needs a positive window")
	case a.Mode != Delay && a.Window != 0:
		return fmt.Errorf("draft approval %s takes no window", a.Mode)
	}
	return nil
}
