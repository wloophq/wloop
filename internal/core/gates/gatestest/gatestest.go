// Package gatestest holds an in-memory gates store, a recorder of the gates'
// consequences, and the suite every gates store passes.
package gatestest

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/core/gates"
	"github.com/wloophq/wloop/internal/domain/gate"
	"github.com/wloophq/wloop/internal/domain/task"
)

// Store is an in-memory gates.Store, gates.Reader, gates.Sent and
// gates.Begun; Now stamps OpenedAt and ClosedAt.
type Store struct {
	mu      sync.Mutex
	gates   []gate.Gate
	applied map[int64]bool
	begun   map[int64]bool
	Now     func() time.Time
}

var (
	_ gates.Store  = (*Store)(nil)
	_ gates.Reader = (*Store)(nil)
	_ gates.Sent   = (*Store)(nil)
	_ gates.Begun  = (*Store)(nil)
)

func NewStore(now func() time.Time) *Store { return &Store{Now: now} }

func (s *Store) Open(_ context.Context, g gate.Gate) (gate.Gate, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	g.ID = int64(len(s.gates) + 1)
	g.Status = gate.Open
	g.OpenedAt = s.Now()
	g.Intent, g.By, g.Rule, g.Answer, g.ClosedAt = "", "", "", "", time.Time{}
	g.Intents = slices.Clone(g.Intents)
	s.gates = append(s.gates, g)
	return clone(g), nil
}

func (s *Store) Resolve(_ context.Context, id int64, r gates.Resolution) (gate.Gate, error) {
	if r.Status == "" || r.Status == gate.Open {
		return gate.Gate{}, fmt.Errorf("resolving gate %d to %q is no resolution", id, r.Status)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if id < 1 || id > int64(len(s.gates)) {
		return gate.Gate{}, fmt.Errorf("%w: %d", gates.ErrNoGate, id)
	}
	g := &s.gates[id-1]
	if g.Status != gate.Open {
		return clone(*g), fmt.Errorf("%w: %d is %s", gates.ErrResolved, id, g.Status)
	}
	g.Status, g.Intent, g.Answer, g.By, g.Rule, g.ClosedAt = r.Status, r.Intent, r.Answer, r.By, r.Rule, s.Now()
	if g.Status == gate.Superseded {
		s.mark(id)
	}
	return clone(*g), nil
}

func (s *Store) mark(id int64) {
	if s.applied == nil {
		s.applied = map[int64]bool{}
	}
	s.applied[id] = true
}

func (s *Store) Applied(_ context.Context, id int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if id < 1 || id > int64(len(s.gates)) {
		return fmt.Errorf("%w: %d", gates.ErrNoGate, id)
	}
	if s.gates[id-1].Status == gate.Open {
		return fmt.Errorf("gate %d is open: no consequence ran", id)
	}
	s.mark(id)
	return nil
}

func (s *Store) Begin(ctx context.Context, id int64) error {
	if err := s.Applied(ctx, id); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.begun == nil {
		s.begun = map[int64]bool{}
	}
	s.begun[id] = true
	return nil
}

func (s *Store) End(_ context.Context, id int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.begun, id)
	return nil
}

func (s *Store) Begun(context.Context) ([]gate.Gate, error) {
	s.mu.Lock()
	begun := maps.Clone(s.begun)
	s.mu.Unlock()
	return s.filter(func(g gate.Gate) bool { return begun[g.ID] }), nil
}

func (s *Store) Unapplied(context.Context) ([]gate.Gate, error) {
	s.mu.Lock()
	applied := maps.Clone(s.applied)
	s.mu.Unlock()
	return s.filter(func(g gate.Gate) bool { return g.Status != gate.Open && !applied[g.ID] }), nil
}

func (s *Store) Get(_ context.Context, id int64) (gate.Gate, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if id < 1 || id > int64(len(s.gates)) {
		return gate.Gate{}, fmt.Errorf("%w: %d", gates.ErrNoGate, id)
	}
	return clone(s.gates[id-1]), nil
}

func (s *Store) OfTask(_ context.Context, id task.ID) ([]gate.Gate, error) {
	return s.filter(func(g gate.Gate) bool { return g.Task == id }), nil
}

func (s *Store) Where(_ context.Context, q gates.Query) ([]gate.Gate, error) {
	statuses := q.Statuses
	if len(statuses) == 0 {
		statuses = []gate.Status{gate.Open}
	}
	return s.filter(func(g gate.Gate) bool {
		return slices.Contains(statuses, g.Status) && (q.Kind == "" || g.Kind == q.Kind) && (q.Name == "" || g.Name == q.Name) &&
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
	g.Intents = slices.Clone(g.Intents)
	s.gates = append(s.gates, g)
	s.mark(g.ID)
	return clone(g), nil
}

func (s *Store) filter(keep func(gate.Gate) bool) []gate.Gate {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []gate.Gate{}
	for _, g := range s.gates {
		if keep(g) {
			out = append(out, clone(g))
		}
	}
	return out
}

func clone(g gate.Gate) gate.Gate {
	g.Intents = slices.Clone(g.Intents)
	return g
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

// StoreContract is the suite every gates store passes. open returns the
// store's three ports over one empty store whose clock reads Epoch.
func StoreContract(t *testing.T, open func(t *testing.T) (gates.Store, gates.Reader, gates.Sent)) {
	ctx := context.Background()
	draft := func(task task.ID) gate.Gate {
		return gate.Gate{Task: task, Kind: gate.KindDraft, Name: "draft", Subject: "7", Intents: []string{"approve", "discard"}, Default: "approve"}
	}
	t.Run("open stores an open gate with an id", func(t *testing.T) {
		s, r, _ := open(t)
		g, err := s.Open(ctx, draft("t-01"))
		if err != nil {
			t.Fatal(err)
		}
		if g.ID == 0 || g.Status != gate.Open || !g.OpenedAt.Equal(Epoch) || g.Intent != "" || !g.ClosedAt.IsZero() {
			t.Fatalf("opened %+v", g)
		}
		got, err := r.Get(ctx, g.ID)
		if err != nil || got.Subject != "7" || !slices.Equal(got.Intents, []string{"approve", "discard"}) || got.Default != "approve" {
			t.Fatalf("get %+v %v", got, err)
		}
	})
	t.Run("a gate with no task keeps none", func(t *testing.T) {
		s, r, _ := open(t)
		g, err := s.Open(ctx, draft(""))
		if err != nil {
			t.Fatal(err)
		}
		if got, err := r.Get(ctx, g.ID); err != nil || got.Task != "" {
			t.Fatalf("get %+v %v", got, err)
		}
	})
	t.Run("resolve closes once with the intent", func(t *testing.T) {
		s, r, _ := open(t)
		g, _ := s.Open(ctx, draft("t-01"))
		res := gates.Resolution{Status: gate.Approved, Intent: "approve", By: task.ActorPerson}
		got, err := s.Resolve(ctx, g.ID, res)
		if err != nil || got.Status != gate.Approved || got.Intent != res.Intent || got.By != task.ActorPerson || got.ClosedAt.IsZero() {
			t.Fatalf("resolved %+v %v", got, err)
		}
		if _, err := s.Resolve(ctx, g.ID, res); !errors.Is(err, gates.ErrResolved) {
			t.Fatalf("a second resolve: %v", err)
		}
		if read, _ := r.Get(ctx, g.ID); read.Intent != res.Intent || read.Status != gate.Approved {
			t.Fatalf("read back %+v", read)
		}
	})
	t.Run("resolving to open is refused", func(t *testing.T) {
		s, r, _ := open(t)
		g, _ := s.Open(ctx, draft("t-01"))
		if _, err := s.Resolve(ctx, g.ID, gates.Resolution{Status: gate.Open, Intent: "approve"}); err == nil {
			t.Fatal("resolved to open")
		}
		if read, _ := r.Get(ctx, g.ID); read.Status != gate.Open || read.Intent != "" {
			t.Fatalf("read back %+v", read)
		}
	})
	t.Run("an unknown gate is ErrNoGate", func(t *testing.T) {
		s, r, _ := open(t)
		if _, err := s.Resolve(ctx, 404, gates.Resolution{Status: gate.Approved}); !errors.Is(err, gates.ErrNoGate) {
			t.Fatalf("resolve: %v", err)
		}
		if _, err := r.Get(ctx, 404); !errors.Is(err, gates.ErrNoGate) {
			t.Fatalf("get: %v", err)
		}
	})
	t.Run("lists are ordered by id", func(t *testing.T) {
		s, r, _ := open(t)
		var ids []int64
		for range 3 {
			g, _ := s.Open(ctx, draft("t-01"))
			ids = append(ids, g.ID)
		}
		if _, err := s.Resolve(ctx, ids[1], gates.Resolution{Status: gate.Rejected, Intent: "discard", By: task.ActorPerson}); err != nil {
			t.Fatal(err)
		}
		of, _ := r.OfTask(ctx, "t-01")
		opened, _ := r.Where(ctx, gates.Query{Task: "t-01"})
		closed, _ := r.Where(ctx, gates.Query{Statuses: []gate.Status{gate.Rejected}})
		if !slices.Equal(idsOf(of), ids) || !slices.Equal(idsOf(opened), []int64{ids[0], ids[2]}) || !slices.Equal(idsOf(closed), []int64{ids[1]}) {
			t.Fatalf("of task %v, open %v, rejected %v", idsOf(of), idsOf(opened), idsOf(closed))
		}
	})
	t.Run("due is at exactly now", func(t *testing.T) {
		s, _, _ := open(t)
		at := draft("t-01")
		at.Deadline = Epoch.Add(time.Hour)
		later := at
		later.Deadline = Epoch.Add(2 * time.Hour)
		a, _ := s.Open(ctx, at)
		_, _ = s.Open(ctx, later)
		_, _ = s.Open(ctx, draft("t-01"))
		due, err := s.Due(ctx, Epoch.Add(time.Hour))
		if err != nil || !slices.Equal(idsOf(due), []int64{a.ID}) {
			t.Fatalf("due %v %v", idsOf(due), err)
		}
	})
	t.Run("a resolved gate is unapplied until marked", func(t *testing.T) {
		s, _, sent := open(t)
		open1, _ := s.Open(ctx, draft("t-01"))
		resolved, _ := s.Open(ctx, draft("t-01"))
		superseded, _ := s.Open(ctx, draft("t-01"))
		if _, err := s.Resolve(ctx, resolved.ID, gates.Resolution{Status: gate.Approved, Intent: "approve", By: task.ActorPerson}); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Resolve(ctx, superseded.ID, gates.Resolution{Status: gate.Superseded, By: task.ActorWorkflow}); err != nil {
			t.Fatal(err)
		}
		if _, err := sent.RecordSent(ctx, gate.Gate{Task: "t-01", Kind: gate.KindPost, By: task.ActorAgent}); err != nil {
			t.Fatal(err)
		}
		got, err := s.Unapplied(ctx)
		if err != nil || !slices.Equal(idsOf(got), []int64{resolved.ID}) || got[0].Status != gate.Approved {
			t.Fatalf("unapplied %v %v, want the resolved gate alone", idsOf(got), err)
		}
		if err := s.Applied(ctx, resolved.ID); err != nil {
			t.Fatal(err)
		}
		if err := s.Applied(ctx, open1.ID); err == nil {
			t.Fatal("an open gate marked applied")
		}
		if got, err := s.Unapplied(ctx); err != nil || len(got) != 0 {
			t.Fatalf("unapplied after the mark %v %v", idsOf(got), err)
		}
	})
	t.Run("a begun consequence is applied and begun until it ends", func(t *testing.T) {
		st, _, _ := open(t)
		s, ok := st.(gates.Begun)
		if !ok {
			t.Skip("the store keeps no begun consequence")
		}
		open1, _ := st.Open(ctx, draft("t-01"))
		resolved, _ := st.Open(ctx, draft("t-01"))
		if _, err := st.Resolve(ctx, resolved.ID, gates.Resolution{Status: gate.Approved, Intent: "approve", By: task.ActorPerson}); err != nil {
			t.Fatal(err)
		}
		if err := s.Begin(ctx, open1.ID); err == nil {
			t.Fatal("an open gate begun")
		}
		if err := s.Begin(ctx, resolved.ID); err != nil {
			t.Fatal(err)
		}
		if got, err := st.Unapplied(ctx); err != nil || len(got) != 0 {
			t.Fatalf("unapplied %v %v: a begun consequence ran", idsOf(got), err)
		}
		if got, err := s.Begun(ctx); err != nil || !slices.Equal(idsOf(got), []int64{resolved.ID}) {
			t.Fatalf("begun %v %v", idsOf(got), err)
		}
		if err := s.End(ctx, resolved.ID); err != nil {
			t.Fatal(err)
		}
		if got, err := s.Begun(ctx); err != nil || len(got) != 0 {
			t.Fatalf("begun after its end %v %v", idsOf(got), err)
		}
	})
	t.Run("a recorded send is approved and never open", func(t *testing.T) {
		_, r, sent := open(t)
		g, err := sent.RecordSent(ctx, gate.Gate{Task: "t-01", Kind: gate.KindPost, By: task.ActorAgent})
		if err != nil || g.Status != gate.Approved {
			t.Fatalf("recorded %+v %v", g, err)
		}
		if opened, _ := r.Where(ctx, gates.Query{}); len(opened) != 0 {
			t.Fatalf("open %+v", opened)
		}
	})
}

// Epoch is the clock a StoreContract store reads.
var Epoch = time.Date(2026, 10, 28, 9, 0, 0, 0, time.UTC)

func idsOf(gs []gate.Gate) []int64 {
	out := []int64{}
	for _, g := range gs {
		out = append(out, g.ID)
	}
	return out
}
