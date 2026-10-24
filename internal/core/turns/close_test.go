package turns_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/core/turns"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	"github.com/wloophq/wloop/internal/domain/turn"
)

func TestMarkDormantRefusesAListedSession(t *testing.T) {
	s, store, reg, sess, rec := newService(t)
	cleanupCase{sess: loopSession("term_1")}.put(reg, store, sess)
	err := s.MarkDormant(context.Background(), "term_1")
	if !refusal(err, "the session's process is still listed") {
		t.Fatalf("MarkDormant = %v", err)
	}
	got, _, _ := reg.Session(context.Background(), "term_1")
	if got.State() != turn.SessionActive || len(kinds(reg.Events(), turns.KindSessionDormant)) != 0 || len(kinds(rec.published, turns.KindSessionDormant)) != 0 {
		t.Fatalf("a listed session went dormant: %+v", got)
	}
}

func TestMarkDormantLeavesAGoneOneOpenWithoutItsHandle(t *testing.T) {
	s, store, reg, sess, rec := newService(t)
	c := cleanupCase{sess: loopSession("term_1"), turns: []turn.Turn{{Seq: 1, Intent: turn.Intent(dintent.Review), ID: "r1", Trigger: turn.Automated, State: turn.Done,
		Session: "term_1", SentTo: "term_1", SentAt: t0.Add(-4 * time.Hour), EndedAt: t0.Add(-3 * time.Hour)}}}
	c.put(reg, store, sess)
	sess.Put(cleanupWS)
	if err := s.MarkDormant(context.Background(), "term_1"); err != nil {
		t.Fatal(err)
	}
	got, _, _ := reg.Session(context.Background(), "term_1")
	if got.State() != turn.SessionDormant || !got.ClosedAt.IsZero() || got.Handle != "" {
		t.Fatalf("session %+v, want dormant", got)
	}
	if evs := kinds(reg.Events(), turns.KindSessionDormant); len(evs) != 1 || evs[0].Key != "term_1:term_1" || len(kinds(rec.published, turns.KindSessionDormant)) != 1 {
		t.Fatalf("events %+v", evs)
	}
	if n := len(kinds(reg.Events(), turns.KindSessionClosed)); n != 0 {
		t.Fatalf("%d session_closed events for a dormant session", n)
	}
	if id, ok, _ := reg.SessionOf(context.Background(), "orca", "term_1"); !ok || id != "term_1" {
		t.Fatalf("the ended handle no longer leads to its session: %q %v", id, ok)
	}
}

func TestMarkDormantNeedsAReadableList(t *testing.T) {
	s, store, reg, sess, _ := newService(t)
	cleanupCase{sess: loopSession("term_1")}.put(reg, store, sess)
	sess.FailReads(errors.New("orca is down"))
	if err := s.MarkDormant(context.Background(), "term_1"); err == nil {
		t.Fatal("MarkDormant marked a session whose list could not be read")
	}
	if got, _, _ := reg.Session(context.Background(), "term_1"); got.State() != turn.SessionActive {
		t.Fatal("the session went dormant")
	}
}

func TestMarkDormantTwiceRecordsOnce(t *testing.T) {
	s, store, reg, sess, _ := newService(t)
	cleanupCase{sess: loopSession("term_1")}.put(reg, store, sess)
	sess.Put(cleanupWS)
	ctx := context.Background()
	for range 2 {
		if err := s.MarkDormant(ctx, "term_1"); err != nil {
			t.Fatal(err)
		}
	}
	if n := len(kinds(reg.Events(), turns.KindSessionDormant)); n != 1 {
		t.Fatalf("%d session_dormant events", n)
	}
}

func TestMarkDormantRefusesWhatItCannotPlace(t *testing.T) {
	s, _, reg, sess, _ := newService(t)
	ctx := context.Background()
	if err := s.MarkDormant(ctx, "term_9"); !refusal(err, "no session term_9") {
		t.Fatalf("unknown session: %v", err)
	}
	reg.Put(turn.Session{ID: "term_2", Manager: "orca", Handle: "term_2", Managed: true})
	if err := s.MarkDormant(ctx, "term_2"); !refusal(err, "no workspace for term_2") {
		t.Fatalf("no workspace: %v", err)
	}
	if n := len(kinds(reg.Events(), turns.KindSessionDormant)); n != 0 || len(sess.Closed()) != 0 {
		t.Fatalf("marked %d", n)
	}
}
