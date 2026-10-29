// Package message holds the subjects of the question, post and proposal
// gates: a turn's question, an agent's post, a proposal. The gate holds the
// decision; a message keeps what was asked and the answer as stored.
package message

import (
	"context"
	"time"

	"github.com/wloophq/wloop/internal/domain/gate"
	"github.com/wloophq/wloop/internal/domain/record"
	"github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
)

type Kind string // question (end of turn), agent_post (task_post), proposal (ledger proposals, workflow decisions; never for a draft)

const (
	Question  Kind = "question"
	AgentPost Kind = "agent_post"
	Proposal  Kind = "proposal"
)

// Gate is the kind of the gate a message of kind k is the subject of.
func (k Kind) Gate() gate.Kind {
	switch k {
	case AgentPost:
		return gate.KindPost
	case Proposal:
		return gate.KindProposal
	}
	return gate.KindQuestion
}

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
	TaskID    string    // the task its gate is on
	Unit      unit.Unit // zero for a message on no unit
	Kind      Kind
	Text      string // <= 4000 bytes, no control characters
	Options   []record.Option
	Recommend string
	Confirm   bool // a yes/no question
	Origin    Origin
	Answer    string // the answer as stored, one line

	// ClosedAt is when its gate closed, zero while it is open.
	CreatedAt, ClosedAt time.Time
}

// Open says whether m is open: its gate waits on the user.
func (m Message) Open() bool { return m.ClosedAt.IsZero() }

type Store interface {
	// Upsert keys a question by (Unit, Origin.Kind, Origin.TurnSeq), a ledger proposal by Origin.Proposal,
	// a workflow or core proposal by (Unit, Origin.Workflow) while open.
	Upsert(ctx context.Context, m Message) (Message, error)
	Message(ctx context.Context, id int64) (Message, bool, error)
	Open(ctx context.Context) ([]Message, error)
	// Close closes an open message with answer at at; on one closed with an answer, a new answer rewords it.
	Close(ctx context.Context, id int64, answer string, at time.Time) error
}

// Conversation is an Inbox message and the user's answers to it, oldest first. Message is zero for the answers
// of a turn stored before each answer named its message.
type Conversation struct {
	Message Message
	Answers []turn.Answer
}
