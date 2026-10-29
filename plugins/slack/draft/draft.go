// Package draft holds the Outbox drafts: posts the loop proposes and the
// user approves before they reach a provider.
package draft

import (
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/wloophq/wloop/plugins/slack/unit"
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

// State is where a draft's delivery stands, as the host keeps it; whether
// it may be posted at all is its gate's decision.
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
}

type Evidence struct {
	Ref       string // Slack: message ref, <conversation>/<ts>; GitHub: comment id
	Permalink string
	At        time.Time
}

type Draft struct {
	ID       ID
	Unit     unit.Unit
	Items    []string // item ids of Unit; may be empty
	Task     string   // the task it serves, "" none; its row in the Outbox view is its draft gate
	Kind     Kind
	Purpose  string   // what it is for: "nudge", "close", "ask_owner", or any label its proposer names
	Delivery Delivery // how it is posted
	Effects  []string // applied when it is sent, such as "task.nudged"
	Target   string   // plugin target grammar; "" means the unit itself (its thread, its PR)
	Text     string   // standard Markdown with mentions as @{<person key>}; the Effects implementation converts both
	Reaction string   // Kind "react" only: emoji name without colons
	Replaced string   // Kind "edit" only: the text the edit replaced
	Original string   // Text as proposed
	Edited   bool     // Text != Original
	Origin   Origin
	State    State
	Evidence Evidence // State "sent" only
	Error    string   // State "failed" only, never a token
	Reason   string   // State "dropped": "user", "assigned", "moot", "dry_run", ...
	Rev      int64    // the stored row's version; every Update bumps it
	// EffectsAppliedAt is when the Effects of a sent draft were applied;
	// zero while they are not.
	EffectsAppliedAt time.Time

	CreatedAt, UpdatedAt, SendingAt time.Time
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

// The reasons a workflow withdraws a draft it proposed, as Reason of the
// discarded draft: its person was assigned the items, or it is moot.
const (
	WithdrawAssigned = "assigned"
	WithdrawMoot     = "moot"
)
