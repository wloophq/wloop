package turns_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/core/turns"
	"github.com/wloophq/wloop/internal/core/turns/turnstest"
	"github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
)

func countingIDs(s *turns.Service) *int {
	n := 0
	s.NewID = func() string {
		n++
		return fmt.Sprintf("sid-%d", n)
	}
	return &n
}

func eventsOf(reg *turnstest.Registry, kind string) []port.Event {
	var out []port.Event
	for _, ev := range reg.Events() {
		if ev.Kind == kind {
			out = append(out, ev)
		}
	}
	return out
}

func session(t *testing.T, reg *turnstest.Registry, id string) turn.Session {
	t.Helper()
	got, ok, err := reg.Session(context.Background(), id)
	if err != nil || !ok {
		t.Fatalf("Session(%s) = %v %v", id, ok, err)
	}
	return got
}

func TestResolveAdoptsAProcessOnce(t *testing.T) {
	ctx := context.Background()
	s, _, reg, _, _ := newService(t)
	minted := countingIDs(s)
	id, err := s.Resolve(ctx, "orca", "h1")
	if err != nil || id != "sid-1" {
		t.Fatalf("Resolve = %q %v", id, err)
	}
	if again, err := s.Resolve(ctx, "orca", "h1"); err != nil || again != id {
		t.Fatalf("Resolve again = %q %v, want %q", again, err, id)
	}
	if *minted != 1 {
		t.Fatalf("minted %d ids", *minted)
	}
	evs := eventsOf(reg, turns.KindSessionAdopted)
	if len(evs) != 1 || evs[0].Key != id || evs[0].Data != "orca:h1" {
		t.Fatalf("adopted events %+v", evs)
	}
	if got := session(t, reg, id); got.Manager != "orca" || got.Handle != "h1" || got.State() != turn.SessionActive {
		t.Fatalf("the adopted session %+v", got)
	}
	if other, err := s.Resolve(ctx, "desktop", "h1"); err != nil || other == id {
		t.Fatalf("another manager's handle h1 resolved to %q %v", other, err)
	}
	if _, err := s.Resolve(ctx, "", "h1"); err == nil {
		t.Fatal("resolved a handle with no manager")
	}
}

func TestAnMCPInitializeBeforeTheHandleIsBound(t *testing.T) {
	ctx := context.Background()

	t.Run("resolve", func(t *testing.T) {
		s, _, reg, _, _ := newService(t)
		countingIDs(s)
		if err := s.MCPSeen(ctx, "h1"); err != nil {
			t.Fatal(err)
		}
		if len(eventsOf(reg, turns.KindMCPSeen)) != 0 {
			t.Fatal("an unbound handle's MCP initialize was saved")
		}
		id, err := s.Resolve(ctx, "orca", "h1")
		if err != nil {
			t.Fatal(err)
		}
		if got := session(t, reg, id); !got.MCPAt.Equal(t0) || !got.Capable() {
			t.Fatalf("the adopted session %+v, want the MCP initialize applied", got)
		}
	})

	t.Run("bind", func(t *testing.T) {
		s, _, reg, _, _ := newService(t)
		reg.Put(turn.Session{ID: "s1", Manager: "orca", StartedAt: t0})
		if err := s.MCPSeen(ctx, "h9"); err != nil {
			t.Fatal(err)
		}
		if err := s.Bind(ctx, "s1", "orca", "h9", "rt"); err != nil {
			t.Fatal(err)
		}
		got := session(t, reg, "s1")
		if got.Handle != "h9" || !got.MCPAt.Equal(t0) || !got.Capable() {
			t.Fatalf("the bound session %+v", got)
		}
		if evs := eventsOf(reg, turns.KindMCPSeen); len(evs) != 1 {
			t.Fatalf("mcp events %+v", evs)
		}

		reg.Put(turn.Session{ID: "s2", Manager: "orca", StartedAt: t0})
		if err := s.Bind(ctx, "s2", "orca", "h9", ""); err != nil {
			t.Fatal(err)
		}
		if evs := eventsOf(reg, turns.KindMCPSeen); len(evs) != 1 {
			t.Fatalf("the queued initialize applied twice: %+v", evs)
		}
		if got := session(t, reg, "s2"); !got.MCPAt.Equal(t0) {
			t.Fatalf("binding s1's process to s2 did not move its sighting: %+v", got)
		}
		if got := session(t, reg, "s1"); got.ClosedAt.IsZero() {
			t.Fatalf("the session that took the process first stays open: %+v", got)
		}
	})
}

func TestWokenBindsAndRestarts(t *testing.T) {
	ctx := context.Background()
	s, _, reg, _, _ := newService(t)
	reg.Put(turn.Session{ID: "s1", Manager: "orca", StartedAt: t0.Add(-2 * time.Hour), IdleSince: t0.Add(-time.Hour), AgentSession: "as-1"})
	s.Clock.(*clock).advance(time.Minute)
	if err := s.Woken(ctx, "s1", "orca", "h2"); err != nil {
		t.Fatal(err)
	}
	got := session(t, reg, "s1")
	if got.Handle != "h2" || got.State() != turn.SessionActive || !got.StartedAt.Equal(t0.Add(time.Minute)) || !got.IdleSince.IsZero() {
		t.Fatalf("the woken session %+v", got)
	}
	if evs := eventsOf(reg, turns.KindSessionWoken); len(evs) != 1 || evs[0].Key != "s1:h2" {
		t.Fatalf("woken events %+v", evs)
	}
}

func TestDormantForPicksTheNewestOfTheUnit(t *testing.T) {
	ctx := context.Background()
	s, _, reg, _, _ := newService(t)
	u := ref(7)
	dormant := func(id string, at time.Time) turn.Session {
		return turn.Session{ID: id, Manager: "orca", WorkspaceID: "w1", Unit: u, AgentSession: "as-" + id, StartedAt: at}
	}
	newer := t0.Add(-time.Minute)
	other := dormant("other-ws", newer)
	other.WorkspaceID = "w2"
	desk := dormant("desktop", newer)
	desk.Manager = "desktop"
	active := dormant("active", newer)
	active.Handle = "h"
	noAgent := dormant("no-agent", newer)
	noAgent.AgentSession = ""
	closed := dormant("closed", newer)
	closed.ClosedAt = t0
	otherUnit := dormant("other-unit", newer)
	otherUnit.Unit = ref(8)
	typed := dormant("typed", t0.Add(-3*time.Hour))
	typed.TypedAt = t0.Add(-time.Hour)
	for _, ses := range []turn.Session{dormant("old", t0.Add(-2*time.Hour)), typed, other, desk, active, noAgent, closed, otherUnit} {
		reg.Put(ses)
	}
	got, at, ok, err := s.DormantFor(ctx, u, "orca", "w1", false)
	if err != nil || !ok || got.ID != "typed" || !at.Equal(t0.Add(-time.Hour)) {
		t.Fatalf("DormantFor = %q %v %v %v", got.ID, at, ok, err)
	}
	if _, _, ok, err := s.DormantFor(ctx, u, "orca", "w9", false); ok || err != nil {
		t.Fatalf("another workspace: %v %v", ok, err)
	}
	if _, _, ok, err := s.DormantFor(ctx, unit.Unit{}, "orca", "w1", false); ok || err != nil {
		t.Fatalf("no unit: %v %v", ok, err)
	}
}

func TestCensusMarksOnlyTheManagersThatListCompletely(t *testing.T) {
	ctx := context.Background()
	s, _, reg, sess, _ := newService(t)
	sc := &scripted{Sessions: sess}
	s.Sessions = sc
	reg.Put(turn.Session{ID: "o1", Manager: "orca", Handle: "oh1", WorkspaceID: "w1", StartedAt: t0.Add(-time.Hour)})
	reg.Put(turn.Session{ID: "d1", Manager: "desktop", Handle: "dh1", WorkspaceID: "w2", StartedAt: t0.Add(-time.Hour)})
	census := func(at time.Time) {
		t.Helper()
		sc.reading = turns.Census{Sessions: map[string]string{}, CompleteFor: map[string]bool{"orca": false, "desktop": true},
			RuntimeIDs: map[string]string{"orca": "ro", "desktop": "rd"}, At: at}
		if err := s.Census(ctx); err != nil {
			t.Fatal(err)
		}
	}
	census(t0)
	census(t0.Add(s.FastJob))
	if !s.ProcessGone("d1") {
		t.Fatal("the session of a complete census is not gone")
	}
	if s.ProcessGone("o1") {
		t.Fatal("the session of an incomplete census is gone")
	}
}

func TestCensusWakesADormantSessionItsManagerListsAgain(t *testing.T) {
	ctx := context.Background()
	s, _, reg, sess, _ := newService(t)
	sc := &scripted{Sessions: sess}
	s.Sessions = sc
	reg.Put(turn.Session{ID: "z", Manager: "orca", WorkspaceID: "w1", StartedAt: t0.Add(-time.Hour), AgentSession: "as-z"})
	sc.reading = turns.Census{Sessions: map[string]string{"z": "w1"}, Complete: true, RuntimeID: "ro", At: t0,
		Processes: map[string]turns.ProcessRef{"z": {Manager: "orca", Handle: "zh"}}}
	if err := s.Census(ctx); err != nil {
		t.Fatal(err)
	}
	got := session(t, reg, "z")
	if got.Handle != "zh" || got.State() != turn.SessionActive || !got.StartedAt.Equal(t0) {
		t.Fatalf("the listed dormant session %+v", got)
	}
	if s.ProcessGone("z") {
		t.Fatal("a woken session is gone")
	}
}

func TestBindMergesASessionAHookTookFirst(t *testing.T) {
	ctx := context.Background()
	s, _, reg, _, _ := newService(t)
	s.NewID = func() string { return "adopted" }
	if _, err := s.Resolve(ctx, "orca", "h1"); err != nil {
		t.Fatal(err)
	}
	if err := s.SessionStarted(ctx, turns.SessionStart{Session: "adopted", AgentSession: "a1", Source: "startup"}); err != nil {
		t.Fatal(err)
	}
	if err := s.MCPSeen(ctx, "h1"); err != nil {
		t.Fatal(err)
	}
	if err := s.Launched(ctx, turn.Session{ID: "minted", Manager: "orca", Handle: "h1", WorkspaceID: "ws"}); err != nil {
		t.Fatal(err)
	}
	got := session(t, reg, "minted")
	if got.Handle != "h1" || got.AgentSession != "a1" || !got.Capable() {
		t.Fatalf("the launched session %+v", got)
	}
	if id, _, _ := reg.SessionOf(ctx, "orca", "h1"); id != "minted" {
		t.Fatalf("h1 leads to %q", id)
	}
	if twin := session(t, reg, "adopted"); twin.ClosedAt.IsZero() {
		t.Fatalf("the adopted twin stays open: %+v", twin)
	}
}
