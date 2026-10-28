package turns_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/core/turns"
	"github.com/wloophq/wloop/internal/domain/gate"
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
	if g := gates.opened[0]; g.Name != "missing-record" || g.Task != id || g.Unit != tu || g.Subject != "run1" {
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
	if b := bookOf(t, rstore, r7); b.Turns[0].State != turn.Unrecorded || len(gates.opened) != 1 {
		t.Fatalf("an intent that requires no record opened %+v", gates.opened)
	}
}
