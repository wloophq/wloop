// Package message holds the Inbox messages: questions, agent posts and proposals.
package message

import (
	"context"
	"time"

	"github.com/wloophq/wloop/internal/domain/record"
	"github.com/wloophq/wloop/internal/domain/unit"
)

type Kind string // question (end of turn), agent_post (inbox_post), proposal (ledger proposals, workflow decisions; never for a draft)

type State string // open, answered, dismissed, superseded

const (
	Question  Kind = "question"
	AgentPost Kind = "agent_post"
	Proposal  Kind = "proposal"
)

const (
	Open       State = "open"
	Answered   State = "answered"
	Dismissed  State = "dismissed"
	Superseded State = "superseded"
)

// The kinds of an Origin.
const (
	OriginTurn     = "turn"
	OriginAgent    = "agent"
	OriginLedger   = "ledger"
	OriginCore     = "core"
	OriginWorkflow = "workflow"
)

// TextMax bounds a message's text in bytes.
const TextMax = 4000

type Origin struct {
	Kind     string // turn, agent, ledger, core, workflow
	TurnSeq  int64  // 0 when no turn
	Session  string
	Workflow string // workflow or core rule name
	Proposal int64  // Kind ledger: the items.Proposal id
}

type Message struct {
	ID        int64
	EntryID   string    // its Inbox entry, opened through gtd.Service.Open
	Unit      unit.Unit // zero for a message on no unit
	Kind      Kind
	Text      string // <= 4000 bytes, no control characters
	Options   []record.Option
	Recommend string
	Confirm   bool // a yes/no question
	Origin    Origin
	State     State
	Answer    string // the answer as stored, one line

	CreatedAt, ClosedAt time.Time
}

type Store interface {
	// Upsert keys a question by (Unit, Origin.Kind, Origin.TurnSeq), a ledger proposal by Origin.Proposal,
	// a workflow or core proposal by (Unit, Origin.Workflow) while open.
	Upsert(ctx context.Context, m Message) (Message, error)
	Message(ctx context.Context, id int64) (Message, bool, error)
	ByEntry(ctx context.Context, entry string) (Message, bool, error)
	Open(ctx context.Context) ([]Message, error)
	Close(ctx context.Context, id int64, st State, answer string, at time.Time) error
}
