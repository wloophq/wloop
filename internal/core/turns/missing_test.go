package turns_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/core/gates"
	"github.com/wloophq/wloop/internal/core/gates/gatestest"
	"github.com/wloophq/wloop/internal/core/messages"
	"github.com/wloophq/wloop/internal/core/messages/messagestest"
	"github.com/wloophq/wloop/internal/core/turns"
	"github.com/wloophq/wloop/internal/core/turns/turnstest"
	"github.com/wloophq/wloop/internal/domain/gate"
	"github.com/wloophq/wloop/internal/domain/record"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/turn"
)

// namedGates records the gates opened by name.
type namedGates struct {
	mu     sync.Mutex
	opened []gate.Gate
}

func (g *namedGates) OpenNamed(_ context.Context, name string, gt gate.Gate) (gate.Gate, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	gt.Name, gt.ID = name, int64(len(g.opened)+1)
	g.opened = append(g.opened, gt)
	return gt, nil
}

func TestARunWithoutItsRequiredReportOpensAGate(t *testing.T) {
	s, store, reg, _, _ := newService(t)
	gates := &namedGates{}
	s.Gates = gates
	ctx := context.Background()
	id := task.ID("t-0123456789abcdef")
	tu := task.UnitOf(id)
	reg.Put(turn.Session{ID: "term_1", AgentSession: "s1", StartedAt: t0.Add(-2 * time.Hour), MCPAt: t0.Add(-2 * time.Hour)})
	store.Put(turn.Book{Unit: tu, Turns: []turn.Turn{{Seq: 1, Intent: "start", ID: "run1", Trigger: turn.Automated, SentTo: "term_1", Session: "term_1",
		State: turn.Started, SentAt: t0.Add(-2 * time.Hour), StartedAt: t0.Add(-time.Hour)}}})
	for _, p := range []string{"p1", "p1"} {
		if _, err := s.Stop(ctx, turns.StopRequest{Session: "term_1", AgentSession: "s1", PromptID: p, LastMessage: "Done, I think."}); err != nil {
			t.Fatal(err)
		}
	}
	if b := bookOf(t, store, tu); b.Turns[0].State != turn.Unrecorded {
		t.Fatalf("the run is %s", b.Turns[0].State)
	}
	if len(gates.opened) != 1 {
		t.Fatalf("gates %+v, want the missing record's", gates.opened)
	}
	if g := gates.opened[0]; g.Name != "missing-record" || g.Task != id || g.Unit != tu || g.Subject != turns.MissingSubject("run1") {
		t.Fatalf("the gate %+v", g)
	}

	r, rstore, rreg, _, _ := newService(t)
	r.Gates = gates
	rreg.Put(turn.Session{ID: "term_1", AgentSession: "s1", StartedAt: t0.Add(-2 * time.Hour), MCPAt: t0.Add(-2 * time.Hour)})
	putTurn(rstore, false)
	for _, p := range []string{"p1", "p1"} {
		if _, err := r.Stop(ctx, turns.StopRequest{Session: "term_1", AgentSession: "s1", PromptID: p, LastMessage: "Should I drop it?"}); err != nil {
			t.Fatal(err)
		}
	}
	if b := bookOf(t, rstore, r7); b.Turns[0].State != turn.Unrecorded || len(gates.opened) != 2 || gates.opened[1].Subject != turns.MissingSubject("abc") {
		t.Fatalf("an intent that names its missing gate and requires no record opened %+v", gates.opened)
	}
}

// storeGates are gates in memory opened by a gates service: by name for the turns, by kind for the messages.
type storeGates struct {
	*gates.Service
	store *gatestest.Store
}

func newStoreGates() storeGates {
	st := gatestest.NewStore(func() time.Time { return t0 })
	question := []gates.Intent{{Name: "answer", Status: gate.Answered, Answer: true}, {Name: "continue", Status: gate.Answered}, {Name: "dismiss", Status: gate.Dismissed}}
	defs := gateProfile{
		{Name: "question", Kind: gate.KindQuestion, Label: "The agent asks", Intents: question},
		{Name: "missing-record", Kind: gate.KindQuestion, Label: "The run stopped", Intents: question},
	}
	return storeGates{gates.New(gates.Deps{Store: st, Reader: st, Profile: defs, Clock: fixed(t0)}), st}
}

func (g storeGates) OpenNamed(ctx context.Context, name string, gt gate.Gate) (gate.Gate, error) {
	gt.Name = name
	return g.Open(ctx, gt, nil)
}

func (g storeGates) Where(ctx context.Context, q gates.Query) ([]gate.Gate, error) {
	return g.store.Where(ctx, q)
}

type gateProfile []gates.Def

func (p gateProfile) Gate(name string) (gates.Def, bool) {
	for _, d := range p {
		if d.Name == name {
			return d, true
		}
	}
	return gates.Def{}, false
}

func (p gateProfile) FirstOf(k gate.Kind) (gates.Def, bool) {
	for _, d := range p {
		if d.Kind == k {
			return d, true
		}
	}
	return gates.Def{}, false
}

func TestOneGateForAnUnrecordedTurn(t *testing.T) {
	failed := record.Record{State: record.Failed, Summary: "the build broke", Resume: "rerun it"}
	for _, c := range []struct {
		name string
		end  func(t *testing.T, s *turns.Service)
	}{
		{"unrecorded", func(t *testing.T, s *turns.Service) {
			for range 2 {
				if _, err := s.Stop(context.Background(), turns.StopRequest{Session: "term_1", AgentSession: "s1", PromptID: "p1", LastMessage: "Done, I think."}); err != nil {
					t.Fatal(err)
				}
			}
		}},
		{"failed", func(t *testing.T, s *turns.Service) {
			if _, err := s.RecordTurnEnd(context.Background(), turns.RecordRequest{Session: "term_1", Turn: "run1", Record: failed}); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			s, store, reg, _, _ := newService(t)
			g := newStoreGates()
			s.Gates = g
			id := task.ID("t-0123456789abcdef")
			tu := task.UnitOf(id)
			reg.Put(turn.Session{ID: "term_1", AgentSession: "s1", StartedAt: t0.Add(-2 * time.Hour), MCPAt: t0.Add(-2 * time.Hour)})
			store.Put(turn.Book{Unit: tu, Turns: []turn.Turn{{Seq: 1, Intent: "start", ID: "run1", Trigger: turn.Automated, SentTo: "term_1", Session: "term_1",
				State: turn.Started, SentAt: t0.Add(-2 * time.Hour), StartedAt: t0.Add(-time.Hour)}}})
			c.end(t, s)
			if opened, _ := g.store.Where(context.Background(), gates.Query{}); len(opened) != 1 {
				t.Fatalf("the turn opened %+v, want its missing gate", opened)
			}
			msgs := &messages.Service{Store: messagestest.New(), Books: store, Turns: s, Gates: g, Intents: turnstest.Intents(), Clock: fixed(t0)}
			if err := msgs.Reconcile(context.Background(), tu); err != nil {
				t.Fatal(err)
			}
			open, err := g.store.Where(context.Background(), gates.Query{})
			if err != nil {
				t.Fatal(err)
			}
			if len(open) != 1 || open[0].Name != "missing-record" || open[0].Subject != turns.MissingSubject("run1") || open[0].Task != id {
				t.Fatalf("open gates %+v", open)
			}
			if m, _ := msgs.Store.Open(context.Background()); len(m) != 0 {
				t.Fatalf("messages %+v", m)
			}
		})
	}
}
