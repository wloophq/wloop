// Package draft holds the Outbox drafts: posts the loop proposes and the
// user approves before they reach a provider.
package draft

import (
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/wloophq/wloop/internal/domain/unit"
)

type ID int64

type Kind string // "reply", "nudge", "close", "react", "ask_owner", "comment", "edit"

const (
	Reply    Kind = "reply"
	Nudge    Kind = "nudge"
	Close    Kind = "close"
	React    Kind = "react"
	AskOwner Kind = "ask_owner"
	Comment  Kind = "comment"
	Edit     Kind = "edit"
)

var Kinds = []Kind{Reply, Nudge, Close, React, AskOwner, Comment, Edit}

func (k Kind) Valid() bool { return slices.Contains(Kinds, k) }

// ConversationKinds are the kinds slack_draft offers on a conversation: all
// but a review comment and an edit, which slack_edit makes at once.
var ConversationKinds = []Kind{Reply, Nudge, Close, AskOwner, React}

// Emoji says whether a draft of kind k posts an emoji, and no text.
func (k Kind) Emoji() bool {
	_, d := ShapeOf(k)
	return d == Reaction
}

// Delivery is how a draft reaches the provider.
type Delivery string // "message", "reaction", "comment", "edit"

const (
	Message         Delivery = "message"
	Reaction        Delivery = "reaction"
	CommentDelivery Delivery = "comment"
	// EditDelivery replaces the text of a message the user posted.
	EditDelivery Delivery = "edit"
)

// ShapeOf is the purpose and delivery of a draft of kind k.
func ShapeOf(k Kind) (purpose string, d Delivery) {
	switch k {
	case React:
		return string(k), Reaction
	case Comment:
		return string(k), CommentDelivery
	case Edit:
		return string(k), EditDelivery
	}
	return string(k), Message
}

// KindOf is the kind readers of Kind see for a purpose and a delivery.
func KindOf(purpose string, d Delivery) Kind {
	switch d {
	case Reaction:
		return React
	case CommentDelivery:
		return Comment
	case EditDelivery:
		return Edit
	}
	switch k := Kind(purpose); k {
	case Nudge, Close, AskOwner:
		return k
	}
	return Reply
}

type State string // "proposed", "approved", "sending", "sent", "failed", "discarded"

const (
	Proposed  State = "proposed"
	Approved  State = "approved"
	Sending   State = "sending"
	Sent      State = "sent"
	Failed    State = "failed"
	Discarded State = "discarded"
)

var States = []State{Proposed, Approved, Sending, Sent, Failed, Discarded}

// Open are the states in which a draft may still be posted.
var Open = []State{Proposed, Approved, Sending}

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
	Key      string // Kind "auto": the rule's dedupe key, e.g. "stalled-nudge:<entry>:<person key>"
	// Unit is the unit of the proposing turn when it is not the draft's, such
	// as a pull request whose turn drafts in a Slack thread; zero when it is.
	Unit unit.Unit
}

type Evidence struct {
	Ref       string // Slack: message ts; GitHub: comment id
	Permalink string
	At        time.Time
}

type WouldPolicy string // "release", "wait"

const (
	WouldRelease WouldPolicy = "release"
	WouldWait    WouldPolicy = "wait"
)

type Outcome string // "", "as_is", "edited", "discarded", "released" (approved by its policy), "dry_run" (stored, never sent)

const (
	OutcomeAsIs      Outcome = "as_is"
	OutcomeEdited    Outcome = "edited"
	OutcomeDiscarded Outcome = "discarded"
	OutcomeReleased  Outcome = "released"
	OutcomeDryRun    Outcome = "dry_run"
)

type Would struct {
	Policy  WouldPolicy // what auto-send would have done, fixed at proposal
	Reason  string      // why, one line
	Outcome Outcome     // what the user (or the policy) did, set at approve or discard
}

type Draft struct {
	ID       ID
	Unit     unit.Unit
	Items    []string // item ids of Unit; may be empty
	Entry    string   // the entry it serves, "" none; the Outbox entry itself is "draft:<ID>"
	Kind     Kind
	Purpose  string   // what it is for: "nudge", "close", "ask_owner", or any label its proposer names
	Delivery Delivery // how it is posted
	Effects  []string // applied when it is sent, such as "entry.nudged"
	Target   string   // plugin target grammar; "" means the unit itself (its thread, its PR)
	Text     string   // standard Markdown with mentions as @{<person key>}; the Effects implementation converts both
	Reaction string   // Kind "react" only: emoji name without colons
	Replaced string   // Kind "edit" only: the text the edit replaced
	Original string   // Text as proposed
	Edited   bool     // Text != Original
	Origin   Origin
	Approval Approval  // fixed at proposal
	Deadline time.Time // Approval.Mode "delay": when Tick approves it; zero otherwise
	State    State
	Evidence Evidence // State "sent" only
	Error    string   // State "failed" only, never a token
	Reason   string   // State "discarded": "user", "assigned", "dry_run", ...
	Would    Would
	Rev      int64 // the stored row's version; every Update bumps it
	// EffectsAppliedAt is when the Effects of a sent draft were applied;
	// zero while they are not.
	EffectsAppliedAt time.Time

	CreatedAt, UpdatedAt, SendingAt time.Time
}

// ErrTransition is wrapped by Can.
var ErrTransition = errors.New("draft: transition not allowed")

// Can says why d cannot move to s, nil when it can:
// proposed->approved|discarded; approved->sending|failed|discarded;
// sending->sent|failed; failed->approved (by hand only)|discarded.
func (d Draft) Can(s State) error {
	if !slices.Contains(transitions[d.State], s) {
		return fmt.Errorf("%w: %s -> %s", ErrTransition, d.State, s)
	}
	return nil
}

var transitions = map[State][]State{
	Proposed: {Approved, Discarded},
	Approved: {Sending, Failed, Discarded},
	Sending:  {Sent, Failed},
	Failed:   {Approved, Discarded},
}

// The reasons a workflow withdraws a draft it proposed, as Reason of the
// discarded draft: its person was assigned the items, or it is moot.
const (
	WithdrawAssigned = "assigned"
	WithdrawMoot     = "moot"
)

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
