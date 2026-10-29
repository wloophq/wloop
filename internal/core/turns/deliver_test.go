package turns_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/core/turns"
	"github.com/wloophq/wloop/internal/core/turns/turnstest"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	"github.com/wloophq/wloop/internal/domain/record"
	"github.com/wloophq/wloop/internal/domain/turn"
)

var working = turns.AgentObservation{WorkspaceID: "ws", Pane: "tab-1:leaf-1", State: turn.AgentWorking}

// busyAsked is an asked turn whose warm, capable session is still working.
func busyAsked(t *testing.T) (*turns.Service, *turnstest.Store, *turnstest.Registry, *turnstest.Sessions, *recorder) {
	t.Helper()
	s, store, reg, sess, rec := newService(t)
	putTurn(store, true)
	reg.Put(turn.Session{ID: "term_1", Pane: "tab-1:leaf-1", WorkspaceID: "ws", AgentSession: "s1", StartedAt: t0.Add(-2 * time.Hour), MCPAt: t0.Add(-2 * time.Hour)})
	sess.Put("ws", turns.Terminal{Session: "term_1", Key: "tab-1", LastOutput: t0.Add(-time.Minute)})
	sess.SetAgents(working)
	return s, store, reg, sess, rec
}

func queue(t *testing.T, s *turns.Service, text string) {
	t.Helper()
	got, err := s.Answer(context.Background(), turns.AnswerRequest{Unit: r7, Text: text, Source: turn.Cockpit})
	if err != nil || got.Route != turn.Queued {
		t.Fatalf("%+v %v", got, err)
	}
}

// sweep is the fast job: Reconcile names the queued answers, Deliver routes
// them.
func sweep(t *testing.T, s *turns.Service) error {
	t.Helper()
	rep, err := s.Reconcile(context.Background(), false)
	if err != nil {
		return err
	}
	return s.Deliver(context.Background(), rep.Queued...)
}

func routes(t *testing.T, store *turnstest.Store) []turn.Route {
	t.Helper()
	var out []turn.Route
	for _, a := range bookOf(t, store, r7).Answers {
		out = append(out, a.Route)
	}
	return out
}

func TestAQueuedAnswerIsTypedWhenItsSessionStops(t *testing.T) {
	s, store, _, sess, rec := busyAsked(t)
	ctx := context.Background()
	queue(t, s, "go ahead")
	if len(sess.Typed()) != 0 {
		t.Fatalf("typed into a busy session: %+v", sess.Typed())
	}
	if err := s.DeliverStopped(ctx, "term_1"); err != nil {
		t.Fatal(err)
	}
	if err := s.DeliverStopped(ctx, "term_1"); err != nil {
		t.Fatal(err)
	}
	if typed := sess.Typed(); len(typed) != 1 || typed[0] != (turnstest.Typed{Handle: "term_1", Line: "answered Gate=0 Unit=https://github.com/acme/api/pull/7"}) {
		t.Fatalf("typed %+v", typed)
	}
	b := bookOf(t, store, r7)
	if a := b.Answers[0]; a.Route != turn.Typed || a.Session != "term_1" {
		t.Fatalf("answer %+v", a)
	}
	if !b.OwesRecord(1) {
		t.Fatal("the typed answer does not make the next stop owe a record")
	}
	routed := kinds(store.Events(), turns.KindAnswerRouted)
	if len(routed) != 2 || routed[0].Key != "1:queued" || routed[1].Key != "1:typed" || routed[1].Data != "typed term_1" {
		t.Fatalf("routed %+v", routed)
	}
	if len(rec.launches) != 0 {
		t.Fatalf("launches %+v", rec.launches)
	}
}

func TestTheStopOfAnotherSessionLeavesTheAnswerQueued(t *testing.T) {
	s, store, _, sess, _ := busyAsked(t)
	queue(t, s, "go ahead")
	if err := s.DeliverStopped(context.Background(), "term_2"); err != nil {
		t.Fatal(err)
	}
	if len(sess.Typed()) != 0 || routes(t, store)[0] != turn.Queued {
		t.Fatalf("typed %+v, routes %v", sess.Typed(), routes(t, store))
	}
}

func TestTheSweepDeliversAQueuedAnswerOnceTheAgentIsIdle(t *testing.T) {
	s, store, _, sess, _ := busyAsked(t)
	queue(t, s, "go ahead")
	if err := sweep(t, s); err != nil {
		t.Fatal(err)
	}
	if len(sess.Typed()) != 0 || routes(t, store)[0] != turn.Queued {
		t.Fatalf("delivered while working: %+v", sess.Typed())
	}
	sess.SetAgents(turns.AgentObservation{WorkspaceID: "ws", Pane: "tab-1:leaf-1", State: turn.AgentDone})
	if err := sweep(t, s); err != nil {
		t.Fatal(err)
	}
	if typed := sess.Typed(); len(typed) != 1 || typed[0].Line != "answered Gate=0 Unit=https://github.com/acme/api/pull/7" || routes(t, store)[0] != turn.Typed {
		t.Fatalf("typed %+v, routes %v", typed, routes(t, store))
	}
}

func TestAQueuedAnswerResumesWhenItsSessionIsGone(t *testing.T) {
	s, store, _, sess, rec := busyAsked(t)
	queue(t, s, "go ahead")
	sess.Put("ws")
	if err := sweep(t, s); err != nil {
		t.Fatal(err)
	}
	want := turns.Launch{Unit: r7, Intent: turn.Intent(dintent.Review), ResumeOf: 1, Answer: "go ahead"}
	if len(sess.Typed()) != 0 || len(rec.launches) != 1 || !reflect.DeepEqual(rec.launches[0], want) || routes(t, store)[0] != turn.Resumed {
		t.Fatalf("typed %+v, launches %+v, routes %v", sess.Typed(), rec.launches, routes(t, store))
	}
}

func TestANewerAnswerReplacesTheQueuedOne(t *testing.T) {
	s, store, _, sess, _ := busyAsked(t)
	queue(t, s, "go ahead")
	queue(t, s, "no, wait\nfor CI")
	if got := routes(t, store); len(got) != 2 || got[0] != turn.Refused || got[1] != turn.Queued {
		t.Fatalf("routes %v", got)
	}
	if err := s.DeliverStopped(context.Background(), "term_1"); err != nil {
		t.Fatal(err)
	}
	if typed := sess.Typed(); len(typed) != 1 || typed[0].Line != "answered Gate=0 Unit=https://github.com/acme/api/pull/7" {
		t.Fatalf("typed %+v", typed)
	}
}

func TestAQueuedAnswerToAQuestionThatClosedIsRefused(t *testing.T) {
	for name, closeIt := range map[string]func(t *testing.T, s *turns.Service, store *turnstest.Store){
		"dismissed": func(t *testing.T, s *turns.Service, _ *turnstest.Store) {
			if err := s.Dismiss(context.Background(), r7); err != nil {
				t.Fatal(err)
			}
		},
		"recorded done": func(t *testing.T, _ *turns.Service, store *turnstest.Store) {
			b := bookOf(t, store, r7)
			b.Record(turn.Record{TurnSeq: 1, Session: "term_1", Hash: "done", At: t0, Acked: true, Record: record.Record{State: record.Done, Summary: "went ahead"}})
			store.Put(b)
		},
	} {
		t.Run(name, func(t *testing.T) {
			s, store, _, sess, rec := busyAsked(t)
			queue(t, s, "go ahead")
			closeIt(t, s, store)
			if err := s.DeliverStopped(context.Background(), "term_1"); err != nil {
				t.Fatal(err)
			}
			if len(sess.Typed()) != 0 || len(rec.launches) != 0 || routes(t, store)[0] != turn.Refused {
				t.Fatalf("typed %+v, launches %+v, routes %v", sess.Typed(), rec.launches, routes(t, store))
			}
		})
	}
}

func TestConcurrentDeliveriesTypeTheAnswerOnce(t *testing.T) {
	s, store, _, sess, _ := busyAsked(t)
	queue(t, s, "go ahead")
	sess.SetAgents()
	var wg sync.WaitGroup
	for _, handle := range []string{"term_1", "", "term_1", ""} {
		wg.Go(func() {
			var err error
			if handle == "" {
				err = s.Deliver(context.Background(), r7)
			} else {
				err = s.DeliverStopped(context.Background(), handle)
			}
			if err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if typed := sess.Typed(); len(typed) != 1 || routes(t, store)[0] != turn.Typed {
		t.Fatalf("typed %+v, routes %v", typed, routes(t, store))
	}
}

func TestAQueuedAnswerTheAgentTakesWithoutStartingIsNotRetyped(t *testing.T) {
	s, store, _, sess, _ := busyAsked(t)
	ctx := context.Background()
	queue(t, s, "go ahead")
	sess.Hold("term_1")
	if err := s.DeliverStopped(ctx, "term_1"); err != nil {
		t.Fatal(err)
	}
	if err := s.DeliverStopped(ctx, "term_1"); err != nil {
		t.Fatal(err)
	}
	if len(sess.Typed()) != 1 || routes(t, store)[0] != turn.Refused {
		t.Fatalf("typed %+v, routes %v", sess.Typed(), routes(t, store))
	}
}

func TestAQueuedAnswerWaitsForTheStoppedAgentsPromptBeforeTyping(t *testing.T) {
	s, store, _, sess, _ := busyAsked(t)
	queue(t, s, "go ahead")
	sess.Settle("term_1")
	if err := s.DeliverStopped(context.Background(), "term_1"); err != nil {
		t.Fatal(err)
	}
	if len(sess.Typed()) != 1 || routes(t, store)[0] != turn.Typed {
		t.Fatalf("typed %+v, routes %v", sess.Typed(), routes(t, store))
	}
}

func TestAQueuedAnswerStaysQueuedWhenTheStoppedAgentsPromptDoesNotReturn(t *testing.T) {
	s, store, _, sess, _ := busyAsked(t)
	queue(t, s, "go ahead")
	sess.NeverIdle("term_1")
	if err := s.DeliverStopped(context.Background(), "term_1"); err != nil {
		t.Fatal(err)
	}
	if len(sess.Typed()) != 0 || routes(t, store)[0] != turn.Queued {
		t.Fatalf("typed %+v, routes %v", sess.Typed(), routes(t, store))
	}
	sess.SetAgents()
	if err := sweep(t, s); err != nil {
		t.Fatal(err)
	}
	if len(sess.Typed()) != 1 || routes(t, store)[0] != turn.Typed {
		t.Fatalf("after the sweep: typed %+v, routes %v", sess.Typed(), routes(t, store))
	}
}

func TestQueuedAnswerWorkspaceGoneRefusedOnce(t *testing.T) {
	s, store, _, sess, rec := busyAsked(t)
	var logs []string
	s.Log = func(format string, args ...any) { logs = append(logs, fmt.Sprintf(format, args...)) }
	queue(t, s, "go ahead")
	sess.Put("ws")
	rec.launchErr = fmt.Errorf("%w: no workspace ws", turns.ErrWorkspaceGone)
	for range 3 {
		if err := sweep(t, s); err != nil {
			t.Fatal(err)
		}
	}
	var routed []port.Event
	for _, e := range kinds(store.Events(), turns.KindAnswerRouted) {
		if strings.HasPrefix(e.Data, "refused") {
			routed = append(routed, e)
		}
	}
	if rec.attempts != 1 || len(routed) != 1 || routed[0].Data != "refused: the workspace is gone" || routes(t, store)[0] != turn.Refused || len(logs) != 1 {
		t.Fatalf("attempts %d, routed %+v, routes %v, logs %q", rec.attempts, routed, routes(t, store), logs)
	}
	if _, err := s.Answer(context.Background(), turns.AnswerRequest{Unit: r7, Text: "again", Source: turn.Cockpit}); !refusal(err, "the workspace is gone") {
		t.Fatalf("the answer was not refused for the gone workspace: %v", err)
	}
}

func TestQueuedAnswerTransientErrorRetries(t *testing.T) {
	s, store, _, sess, rec := busyAsked(t)
	queue(t, s, "go ahead")
	sess.Put("ws")
	rec.launchErr = errors.New("orca is down")
	for range 3 {
		if err := sweep(t, s); err == nil {
			t.Fatal("the transient error was swallowed")
		}
	}
	if rec.attempts != 3 || routes(t, store)[0] != turn.Queued {
		t.Fatalf("attempts %d, routes %v", rec.attempts, routes(t, store))
	}
}
