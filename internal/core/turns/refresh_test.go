package turns_test

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/core/turns"
	"github.com/wloophq/wloop/internal/domain/turn"
)

func TestAnswerRouteRefreshes(t *testing.T) {
	s, store, reg, sess, _ := newService(t)
	putTurn(store, true)
	reg.Put(turn.Session{ID: "term_1", Pane: "tab-1:leaf-1", WorkspaceID: "ws", AgentSession: "s1", StartedAt: t0.Add(-2 * time.Hour), MCPAt: t0.Add(-2 * time.Hour)})
	sess.Put("ws", turns.Terminal{Session: "term_1", Key: "tab-1", LastOutput: t0.Add(-5 * time.Minute)})
	got, err := s.Answer(context.Background(), turns.AnswerRequest{Unit: r7, Text: "go ahead", Source: turn.Cockpit})
	if err != nil || got.Route != turn.Typed {
		t.Fatalf("route %+v, %v", got, err)
	}
	if n := sess.Refreshes(); n != 1 {
		t.Fatalf("routing the answer refreshed the sessions %d times, want 1", n)
	}
}

func TestMarkDormantRefusesAnIncompleteCensus(t *testing.T) {
	s, store, reg, sess, _ := newService(t)
	cleanupCase{sess: loopSession("term_1")}.put(reg, store, sess)
	sess.Put(cleanupWS)
	sess.Partial()
	if err := s.MarkDormant(context.Background(), "term_1"); !refusal(err, "incomplete") {
		t.Fatalf("MarkDormant = %v", err)
	}
	if got, _, _ := reg.Session(context.Background(), "term_1"); got.State() != turn.SessionActive {
		t.Fatal("the session went dormant")
	}
	if sess.Refreshes() != 1 {
		t.Fatalf("MarkDormant decided on %d refreshed reads, want 1", sess.Refreshes())
	}
}

func TestStartupGoneSessionsFromTheCensus(t *testing.T) {
	s, _, reg, sess, _ := newService(t)
	reg.Put(turn.Session{ID: "term_1", WorkspaceID: "ws"})
	reg.Put(turn.Session{ID: "term_2", WorkspaceID: "ws"})
	reg.Put(turn.Session{ID: "term_3"})
	sess.Put("other", turns.Terminal{Session: "term_1"})
	rep, err := s.Reconcile(context.Background(), true)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(rep.GoneSessions, []string{"term_2"}) {
		t.Fatalf("gone %v, want term_2: the census lists term_1 in any workspace and term_3 has none", rep.GoneSessions)
	}
}

func TestStartupCountsNoSessionGoneOnAnIncompleteCensus(t *testing.T) {
	s, _, reg, sess, _ := newService(t)
	reg.Put(turn.Session{ID: "term_2", WorkspaceID: "ws"})
	sess.Partial()
	rep, err := s.Reconcile(context.Background(), true)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.GoneSessions) != 0 || len(rep.Warnings) != 1 {
		t.Fatalf("gone %v, warnings %v", rep.GoneSessions, rep.Warnings)
	}
}
