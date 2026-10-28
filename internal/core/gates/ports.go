// Package gates holds every decision waiting on the user: it opens a gate,
// resolves it only through the user API or a trusted policy rule, and
// applies the one consequence its kind names.
package gates

import (
	"context"
	"errors"
	"time"

	"github.com/wloophq/wloop/internal/domain/gate"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
)

// Store keeps the gates. Its lists are ordered by ID, oldest first; a list
// nothing matches is empty.
type Store interface {
	// Open stores g as open with a new ID and OpenedAt, and returns it.
	Open(ctx context.Context, g gate.Gate) (gate.Gate, error)
	// Resolve closes the open gate id with r and returns it: ErrNoGate when
	// there is none, ErrResolved when it is closed already.
	Resolve(ctx context.Context, id int64, r Resolution) (gate.Gate, error)
	// OfTask are task id's gates, open and closed.
	OfTask(ctx context.Context, id task.ID) ([]gate.Gate, error)
	// Opened are the open gates matching q.
	Opened(ctx context.Context, q Query) ([]gate.Gate, error)
	// Due are the open gates whose Deadline is set and not after now.
	Due(ctx context.Context, now time.Time) ([]gate.Gate, error)
}

// Resolution is how a gate closed: Intent is the intent a person or a rule
// chose, Answer a question's text, By who resolved it, Rule the policy rule
// that released it ("window" for a deadline's default), "" for a person.
type Resolution struct {
	Status gate.Status
	Intent string
	Answer string
	By     task.Actor
	Rule   string
}

// Query narrows Opened: a zero field matches every gate.
type Query struct {
	Kind gate.Kind
	Name string
	Task task.ID
	Unit unit.Unit
}

// The consequences of a resolved gate, one port per kind.
type (
	// Publisher publishes the draft of a resolved draft gate.
	Publisher interface {
		Publish(ctx context.Context, g gate.Gate) error
	}
	// Releaser releases the plan item of a resolved plan gate.
	Releaser interface {
		Release(ctx context.Context, g gate.Gate) error
	}
	// Deliverer delivers a resolved question or post gate's answer to its
	// session.
	Deliverer interface {
		Deliver(ctx context.Context, g gate.Gate) error
	}
	// Decider applies the operations of a resolved proposal gate.
	Decider interface {
		Decide(ctx context.Context, g gate.Gate) error
	}
	// Unblocker unblocks the first run a resolved trust gate held.
	Unblocker interface {
		Unblock(ctx context.Context, g gate.Gate) error
	}
)

// Sent records a post an agent made at once with a tool as a gate already
// approved. It is no resolution: no agent resolves an open gate.
type Sent interface {
	RecordSent(ctx context.Context, g gate.Gate) (gate.Gate, error)
}

var (
	ErrNoGate   = errors.New("no such gate")
	ErrResolved = errors.New("the gate is resolved already")
)
