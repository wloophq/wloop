package turns_test

import (
	"context"
	"testing"
	"testing/synctest"

	"github.com/wloophq/wloop/internal/core/turns"
	"github.com/wloophq/wloop/internal/core/turns/turnstest"
	"github.com/wloophq/wloop/internal/domain/turn"
)

// lookupGate runs bind once, right after an MCP sighting looked its handle up
// and before the sighting acts on what it found. With the lookup outside the
// service's lock, the bind runs to its end in that window.
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
		bind func(*testing.T, *turns.Service, *turnstest.Registry) (string, func() error)
	}{
		{"resolve", func(_ *testing.T, s *turns.Service, _ *turnstest.Registry) (string, func() error) {
			s.NewID = func() string { return "adopted" }
			return "adopted", func() error { _, err := s.Resolve(ctx, "orca", "h1"); return err }
		}},
		{"bind", func(_ *testing.T, s *turns.Service, reg *turnstest.Registry) (string, func() error) {
			reg.Put(turn.Session{ID: "launched", Manager: "orca", StartedAt: t0})
			return "launched", func() error { return s.Bind(ctx, "launched", "orca", "h1", "rt") }
		}},
		{"bind merging the adopted twin", func(t *testing.T, s *turns.Service, reg *turnstest.Registry) (string, func() error) {
			s.NewID = func() string { return "adopted" }
			if _, err := s.Resolve(ctx, "orca", "h1"); err != nil {
				t.Fatal(err)
			}
			reg.Put(turn.Session{ID: "launched", Manager: "orca", StartedAt: t0})
			return "launched", func() error { return s.Bind(ctx, "launched", "orca", "h1", "rt") }
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				s, _, reg, _, _ := newService(t)
				id, bind := tc.bind(t, s, reg)
				gate := &lookupGate{Registry: reg, svc: s, bind: bind}
				s.Registry = gate
				t.Cleanup(func() {
					if gate.done != nil {
						<-gate.done
					}
				})
				if err := s.MCPSeen(ctx, "h1"); err != nil {
					t.Fatal(err)
				}
				if gate.done == nil {
					t.Fatal("the MCP sighting never looked its handle up")
				}
				if err := <-gate.done; err != nil {
					t.Fatal(err)
				}
				gate.done <- nil
				if got := session(t, reg, id); got.Handle != "h1" || !got.MCPAt.Equal(t0) || !got.Capable() {
					t.Fatalf("the session bound while its MCP initialize was seen: %+v", got)
				}
				if evs := eventsOf(reg, turns.KindMCPSeen); len(evs) != 1 {
					t.Fatalf("mcp events %+v", evs)
				}
			})
		})
	}
}
