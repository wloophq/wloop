package turns_test

import (
	"context"
	"testing"

	"github.com/wloophq/wloop/internal/core/turns"
	"github.com/wloophq/wloop/internal/core/turns/turnstest"
	"github.com/wloophq/wloop/internal/domain/turn"
)

// lookupGate runs bind once, right after an MCP sighting looked its handle up
// and before the sighting acts on what it found.
type lookupGate struct {
	*turnstest.Registry
	svc  *turns.Service
	bind func() error
	done chan error
}

func (g *lookupGate) SessionOfHandle(ctx context.Context, handle string) (string, string, bool, error) {
	id, manager, ok, err := g.Registry.SessionOfHandle(ctx, handle)
	if g.done != nil {
		return id, manager, ok, err
	}
	g.done = make(chan error, 1)
	go func() { g.done <- g.bind() }()
	if !g.svc.Locked() {
		g.done <- <-g.done
	}
	return id, manager, ok, err
}

func TestAnMCPInitializeRacingTheBindOfItsHandle(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name string
		bind func(*turns.Service, *turnstest.Registry) (string, func() error)
	}{
		{"resolve", func(s *turns.Service, _ *turnstest.Registry) (string, func() error) {
			s.NewID = func() string { return "adopted" }
			return "adopted", func() error { _, err := s.Resolve(ctx, "orca", "h1"); return err }
		}},
		{"bind", func(s *turns.Service, reg *turnstest.Registry) (string, func() error) {
			reg.Put(turn.Session{ID: "launched", Manager: "orca", StartedAt: t0})
			return "launched", func() error { return s.Bind(ctx, "launched", "orca", "h1", "rt") }
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, _, reg, _, _ := newService(t)
			id, bind := tc.bind(s, reg)
			gate := &lookupGate{Registry: reg, svc: s, bind: bind}
			s.Registry = gate
			if err := s.MCPSeen(ctx, "h1"); err != nil {
				t.Fatal(err)
			}
			if err := <-gate.done; err != nil {
				t.Fatal(err)
			}
			if got := session(t, reg, id); got.Handle != "h1" || !got.MCPAt.Equal(t0) || !got.Capable() {
				t.Fatalf("the session bound while its MCP initialize was seen: %+v", got)
			}
			if evs := eventsOf(reg, turns.KindMCPSeen); len(evs) != 1 {
				t.Fatalf("mcp events %+v", evs)
			}
		})
	}
}
