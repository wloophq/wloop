package outbox

import (
	"context"

	"github.com/wloophq/wloop/internal/core/gates"
	coretasks "github.com/wloophq/wloop/internal/core/tasks"
	"github.com/wloophq/wloop/internal/domain/decision"
	"github.com/wloophq/wloop/internal/domain/gate"
	"github.com/wloophq/wloop/internal/domain/task"
)

// Waiting is what the gates and the drafts make the tasks wait on, by task: their open gates but the draft gates
// an open message pins, which wait on that message's question, and the approved gates of their drafts whose
// delivery failed. Pinned are the pins, by draft, with the message that pins each.
type Waiting struct {
	Pinned      map[int64]int64
	open        map[task.ID][]gate.Gate
	undelivered map[task.ID][]gate.Gate
}

// WaitingReads are where Waiting is read from: the gates a query matches, the pins (nil: none) and the Outbox's
// drafts (nil: no failed delivery). Without Where nothing waits.
type WaitingReads struct {
	Where  func(context.Context, gates.Query) ([]gate.Gate, error)
	Pinned func(context.Context) (map[int64]int64, error)
	Drafts Lister
}

const (
	passWaiting   = "outbox.waiting"
	passOpenGates = "outbox.open_gates"
)

// OpenGates are every open gate where matches, by task, in the order it returns them, read once in the pass ctx
// carries, which ReadWaiting shares.
func OpenGates(ctx context.Context, where func(context.Context, gates.Query) ([]gate.Gate, error)) (map[task.ID][]gate.Gate, error) {
	return coretasks.Once(ctx, passOpenGates, func() (map[task.ID][]gate.Gate, error) {
		gs, err := where(ctx, gates.Query{Statuses: []gate.Status{gate.Open}})
		if err != nil {
			return nil, err
		}
		out := map[task.ID][]gate.Gate{}
		for _, g := range gs {
			out[g.Task] = append(out[g.Task], g)
		}
		return out, nil
	})
}

// ReadWaiting reads Waiting from r, once in the pass ctx carries, so the task facts and the queues that read it in
// one pass share one read.
func ReadWaiting(ctx context.Context, r WaitingReads) (Waiting, error) {
	return coretasks.Once(ctx, passWaiting, func() (Waiting, error) {
		return r.read(ctx, func() (map[task.ID][]gate.Gate, error) { return OpenGates(ctx, r.Where) })
	})
}

// ReadWaitingOf is what task id, whose open gates are open, waits on: in the pass ctx carries ReadWaiting's, which
// reads every open gate once for the pass, and outside one read with open alone.
func ReadWaitingOf(ctx context.Context, r WaitingReads, id task.ID, open []gate.Gate) (Waiting, error) {
	if _, ok := coretasks.PassOf(ctx); ok {
		return ReadWaiting(ctx, r)
	}
	return r.read(ctx, func() (map[task.ID][]gate.Gate, error) { return map[task.ID][]gate.Gate{id: open}, nil })
}

func (r WaitingReads) read(ctx context.Context, open func() (map[task.ID][]gate.Gate, error)) (Waiting, error) {
	var w Waiting
	if r.Where == nil {
		return w, nil
	}
	var err error
	if r.Pinned != nil {
		if w.Pinned, err = r.Pinned(ctx); err != nil {
			return Waiting{}, err
		}
	}
	if w.open, err = open(); err != nil {
		return Waiting{}, err
	}
	if r.Drafts != nil {
		if w.undelivered, err = Undelivered(ctx, r.Drafts, r.Where); err != nil {
			return Waiting{}, err
		}
	}
	return w, nil
}

// Of are the decisions task id waits on, as m maps its gates and its linked units' sources.
func (w Waiting) Of(m decision.Map, id task.ID, sources []decision.Source) []decision.Found {
	return m.Of(gates.Unpinned(w.open[id], w.Pinned), w.undelivered[id], sources)
}
