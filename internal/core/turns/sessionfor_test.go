package turns_test

import (
	"context"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/core/turns"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	"github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
)

var (
	threadA = unit.Unit{Provider: "slack", Key: "T000/C001/1.1"}
	threadB = unit.Unit{Provider: "slack", Key: "T000/C001/2.2"}
)

func TestSessionForPicksTheUnitsLiveSession(t *testing.T) {
	s, _, reg, sess, _ := newService(t)
	ctx := context.Background()
	reg.Put(turn.Session{ID: "s1", WorkspaceID: "ws", Unit: threadA, StartedAt: t0})
	reg.Put(turn.Session{ID: "s1-old", WorkspaceID: "ws", Unit: threadA, StartedAt: t0.Add(-time.Hour)})
	reg.Put(turn.Session{ID: "s2", WorkspaceID: "ws", Unit: threadB, StartedAt: t0.Add(time.Minute)})
	reg.Put(turn.Session{ID: "s1-other-ws", WorkspaceID: "ws-2", Unit: threadA, StartedAt: t0.Add(time.Hour)})
	reg.Put(turn.Session{ID: "s1-closed", WorkspaceID: "ws", Unit: threadA, StartedAt: t0.Add(time.Hour), ClosedAt: t0})
	sess.Put("ws", turns.Terminal{Session: "s1", LastOutput: t0.Add(-time.Minute)}, turns.Terminal{Session: "s1-old"}, turns.Terminal{Session: "s2", LastOutput: t0},
		turns.Terminal{Session: "s1-closed"})
	sess.Put("ws-2", turns.Terminal{Session: "s1-other-ws"})
	got, ok, err := s.SessionFor(ctx, threadA, "ws")
	if err != nil || !ok || got.Session.ID != "s1" || !got.LastOutput.Equal(t0.Add(-time.Minute)) {
		t.Fatalf("SessionFor = %+v %v %v", got, ok, err)
	}
	if got, ok, _ := s.SessionFor(ctx, threadB, "ws"); !ok || got.Session.ID != "s2" {
		t.Fatalf("B: %+v %v", got, ok)
	}
	if _, ok, _ := s.SessionFor(ctx, unit.Unit{Provider: "slack", Key: "none"}, "ws"); ok {
		t.Fatal("a unit without a session found one")
	}
}

func TestSessionForSkipsAHandleTheManagerNoLongerLists(t *testing.T) {
	s, _, reg, sess, _ := newService(t)
	reg.Put(turn.Session{ID: "s1", WorkspaceID: "ws", Unit: threadA, StartedAt: t0})
	sess.Put("ws", turns.Terminal{Session: "s2"})
	if got, ok, err := s.SessionFor(context.Background(), threadA, "ws"); ok || err != nil {
		t.Fatalf("SessionFor = %+v %v %v", got, ok, err)
	}
}

func TestLaunchedAndFirstTurnSetTheSessionsUnit(t *testing.T) {
	s, _, reg, _, _ := newService(t)
	ctx := context.Background()
	if err := s.Launched(ctx, turn.Session{ID: "launched", WorkspaceID: "ws", Unit: threadA}); err != nil {
		t.Fatal(err)
	}
	if got, _, _ := reg.Session(ctx, "launched"); got.Unit != threadA {
		t.Fatalf("launched: %+v", got)
	}
	if err := s.SessionStarted(ctx, turns.SessionStart{Session: "hook", AgentSession: "a", Source: "startup"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Send(ctx, turns.SendRequest{Unit: r7, Intent: turn.Intent(dintent.Review), Turn: "r1", Trigger: turn.Automated, Records: true}); err != nil {
		t.Fatal(err)
	}
	if err := s.SentTo(ctx, r7, "r1", "hook"); err != nil {
		t.Fatal(err)
	}
	if got, _, _ := reg.Session(ctx, "hook"); got.Unit != r7 {
		t.Fatalf("hook session after its first turn: %+v", got)
	}
	if _, err := s.Send(ctx, turns.SendRequest{Unit: threadB, Intent: turn.Intent(dintent.Review), Turn: "r2", Trigger: turn.Automated, Records: true}); err != nil {
		t.Fatal(err)
	}
	if err := s.SentTo(ctx, threadB, "r2", "hook"); err != nil {
		t.Fatal(err)
	}
	if got, _, _ := reg.Session(ctx, "hook"); got.Unit != r7 {
		t.Fatalf("a second turn moved the unit: %+v", got)
	}
	if err := s.SessionStarted(ctx, turns.SessionStart{Session: "byhand", AgentSession: "b", Source: "startup"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.StartByHand(ctx, turns.SendRequest{Unit: threadB, Intent: turn.Intent(dintent.Address), Turn: "r3", Trigger: turn.ByHand, Records: true}, "byhand"); err != nil {
		t.Fatal(err)
	}
	if got, _, _ := reg.Session(ctx, "byhand"); got.Unit != threadB {
		t.Fatalf("by hand: %+v", got)
	}
}
