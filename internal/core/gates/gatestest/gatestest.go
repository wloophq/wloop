// Package gatestest holds an in-memory gates.Store and a recorder of the
// gates' consequences.
package gatestest

import (
	"context"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/wloophq/wloop/internal/core/gates"
	"github.com/wloophq/wloop/internal/domain/gate"
	"github.com/wloophq/wloop/internal/domain/task"
)

// Store is an in-memory gates.Store and gates.Sent; Now stamps OpenedAt and
// ClosedAt.
type Store struct {
	mu    sync.Mutex
	gates []gate.Gate
	Now   func() time.Time
}

var (
	_ gates.Store = (*Store)(nil)
	_ gates.Sent  = (*Store)(nil)
)

func NewStore(now func() time.Time) *Store { return &Store{Now: now} }

func (s *Store) Open(_ context.Context, g gate.Gate) (gate.Gate, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	g.ID = int64(len(s.gates) + 1)
	g.Status = gate.Open
	g.OpenedAt = s.Now()
	g.By, g.Rule, g.Answer, g.ClosedAt = "", "", "", time.Time{}
	s.gates = append(s.gates, g)
	return g, nil
}

func (s *Store) Resolve(_ context.Context, id int64, r gates.Resolution) (gate.Gate, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if id < 1 || int(id) > len(s.gates) {
		return gate.Gate{}, fmt.Errorf("%w: %d", gates.ErrNoGate, id)
	}
	g := &s.gates[id-1]
	if g.Status != gate.Open {
		return *g, fmt.Errorf("%w: %d is %s", gates.ErrResolved, id, g.Status)
	}
	g.Status, g.Answer, g.By, g.Rule, g.ClosedAt = r.Status, r.Answer, r.By, r.Rule, s.Now()
	return *g, nil
}

func (s *Store) OfTask(_ context.Context, id task.ID) ([]gate.Gate, error) {
	return s.filter(func(g gate.Gate) bool { return g.Task == id }), nil
}

func (s *Store) Opened(_ context.Context, q gates.Query) ([]gate.Gate, error) {
	return s.filter(func(g gate.Gate) bool {
		return g.Status == gate.Open && (q.Kind == "" || g.Kind == q.Kind) && (q.Name == "" || g.Name == q.Name) &&
			(q.Task == "" || g.Task == q.Task) && (q.Unit.IsZero() || g.Unit == q.Unit)
	}), nil
}

func (s *Store) Due(_ context.Context, now time.Time) ([]gate.Gate, error) {
	return s.filter(func(g gate.Gate) bool {
		return g.Status == gate.Open && !g.Deadline.IsZero() && !g.Deadline.After(now)
	}), nil
}

// RecordSent stores g closed as approved, opened and closed now.
func (s *Store) RecordSent(_ context.Context, g gate.Gate) (gate.Gate, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	g.ID = int64(len(s.gates) + 1)
	g.Status = gate.Approved
	g.OpenedAt, g.ClosedAt = s.Now(), s.Now()
	s.gates = append(s.gates, g)
	return g, nil
}

func (s *Store) filter(keep func(gate.Gate) bool) []gate.Gate {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []gate.Gate{}
	for _, g := range s.gates {
		if keep(g) {
			g.Intents = slices.Clone(g.Intents)
			out = append(out, g)
		}
	}
	return out
}

// Consequences records each consequence applied, in order, as "<port> <id>",
// such as "publish 3"; Err, when set, is what every call returns.
type Consequences struct {
	mu    sync.Mutex
	Calls []string
	Err   error
}

var (
	_ gates.Publisher = (*Consequences)(nil)
	_ gates.Releaser  = (*Consequences)(nil)
	_ gates.Deliverer = (*Consequences)(nil)
	_ gates.Decider   = (*Consequences)(nil)
	_ gates.Unblocker = (*Consequences)(nil)
)

func (c *Consequences) record(port string, g gate.Gate) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.Calls = append(c.Calls, fmt.Sprintf("%s %d", port, g.ID))
	return c.Err
}

func (c *Consequences) Publish(_ context.Context, g gate.Gate) error { return c.record("publish", g) }
func (c *Consequences) Release(_ context.Context, g gate.Gate) error { return c.record("release", g) }
func (c *Consequences) Deliver(_ context.Context, g gate.Gate) error { return c.record("deliver", g) }
func (c *Consequences) Decide(_ context.Context, g gate.Gate) error  { return c.record("decide", g) }
func (c *Consequences) Unblock(_ context.Context, g gate.Gate) error { return c.record("unblock", g) }
