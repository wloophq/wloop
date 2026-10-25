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

type Kind string // "reply", "nudge", "close", "react", "ask_owner", "comment"

const (
	Reply    Kind = "reply"
	Nudge    Kind = "nudge"
	Close    Kind = "close"
	React    Kind = "react"
	AskOwner Kind = "ask_owner"
	Comment  Kind = "comment"
)

var Kinds = []Kind{Reply, Nudge, Close, React, AskOwner, Comment}

func (k Kind) Valid() bool { return slices.Contains(Kinds, k) }

// Delivery is how a draft reaches the provider.
type Delivery string // "message", "reaction", "comment"

const (
	Message         Delivery = "message"
	Reaction        Delivery = "reaction"
	CommentDelivery Delivery = "comment"
)

// ShapeOf is the purpose and delivery of a draft of kind k.
func ShapeOf(k Kind) (purpose string, d Delivery) {
	switch k {
	case React:
		return string(k), Reaction
	case Comment:
		return string(k), CommentDelivery
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
}

type Evidence struct {
	Ref       string // Slack: message ts; GitHub: comment id
	Permalink string
	At        time.Time
}

type WouldPolicy string // "send", "hold"

const (
	WouldSend WouldPolicy = "send"
	WouldHold WouldPolicy = "hold"
)

type Outcome string // "", "as_is", "edited", "discarded", "auto" (approved by its policy), "observed" (recorded only)

const (
	OutcomeAsIs      Outcome = "as_is"
	OutcomeEdited    Outcome = "edited"
	OutcomeDiscarded Outcome = "discarded"
	OutcomeAuto      Outcome = "auto"
	OutcomeObserved  Outcome = "observed"
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
	Original string   // Text as proposed
	Edited   bool     // Text != Original
	Origin   Origin
	Approval Approval  // fixed at proposal
	Deadline time.Time // Approval.Mode "window": when Tick approves it; zero otherwise
	State    State
	Evidence Evidence // State "sent" only
	Error    string   // State "failed" only, never a token
	Reason   string   // State "discarded": "user", "assigned", "observe", ...
	Would    Would
	Rev      int64 // the stored row's version; every Update bumps it

	CreatedAt, UpdatedAt, SendingAt time.Time
}

// ErrTransition is wrapped by Can.
var ErrTransition = errors.New("draft: transition not allowed")

// Can says why d cannot move to s, nil when it can:
// proposed->approved|discarded; approved->sending|discarded;
// sending->sent|failed; failed->approved (by hand only)|discarded.
func (d Draft) Can(s State) error {
	if !slices.Contains(transitions[d.State], s) {
		return fmt.Errorf("%w: %s -> %s", ErrTransition, d.State, s)
	}
	return nil
}

var transitions = map[State][]State{
	Proposed: {Approved, Discarded},
	Approved: {Sending, Discarded},
	Sending:  {Sent, Failed},
	Failed:   {Approved, Discarded},
}

type ApprovalMode string // "always" (default), "window", "none", "observe"

const (
	Always  ApprovalMode = "always"
	Window  ApprovalMode = "window"
	None    ApprovalMode = "none"
	Observe ApprovalMode = "observe"
)

type Approval struct {
	Mode   ApprovalMode
	Window time.Duration // Mode "window" only, > 0
}

// Validate refuses an unknown mode, a window <= 0 with window, a window set without window.
func (a Approval) Validate() error {
	switch {
	case !slices.Contains([]ApprovalMode{Always, Window, None, Observe}, a.Mode):
		return fmt.Errorf("unknown draft approval mode %q", a.Mode)
	case a.Mode == Window && a.Window <= 0:
		return errors.New("draft approval window needs a positive window")
	case a.Mode != Window && a.Window != 0:
		return fmt.Errorf("draft approval %s takes no window", a.Mode)
	}
	return nil
}
