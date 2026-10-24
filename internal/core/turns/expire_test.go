package turns_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/core/turns"
	"github.com/wloophq/wloop/internal/core/turns/turnstest"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	"github.com/wloophq/wloop/internal/domain/turn"
)

// scripted answers Census with whatever the test set last.
type scripted struct {
	*turnstest.Sessions
	reading turns.Census
	err     error
	termErr error
}

func (s *scripted) Census(context.Context) (turns.Census, error) { return s.reading, s.err }

func (s *scripted) Terminals(ctx context.Context, workspaceID string) ([]turns.Terminal, error) {
	if s.termErr != nil {
		return nil, s.termErr
	}
	return s.Sessions.Terminals(ctx, workspaceID)
}

func (s *scripted) complete(at time.Time, runtime string, listed ...string) {
	c := turns.Census{Sessions: map[string]string{}, Complete: true, RuntimeID: runtime, At: at}
	for _, h := range listed {
		c.Sessions[h] = cleanupWS
	}
	s.reading, s.err = c, nil
}

func expireService(t *testing.T, open ...string) (*turns.Service, *turnstest.Registry, *scripted, *recorder) {
	t.Helper()
	s, _, reg, sess, rec := newService(t)
	sc := &scripted{Sessions: sess}
	s.Sessions = sc
	for _, h := range open {
		reg.Put(turn.Session{ID: h, WorkspaceID: cleanupWS, StartedAt: t0.Add(-time.Hour)})
	}
	return s, reg, sc, rec
}

// gone runs a census and names the open sessions it finds gone.
func gone(t *testing.T, s *turns.Service, reg *turnstest.Registry) []string {
	t.Helper()
	if err := s.Census(context.Background()); err != nil {
		t.Fatal(err)
	}
	return goneNow(t, s, reg)
}

func goneNow(t *testing.T, s *turns.Service, reg *turnstest.Registry) []string {
	t.Helper()
	var out []string
	for _, h := range openHandles(t, reg) {
		if s.ProcessGone(h) {
			out = append(out, h)
		}
	}
	return out
}

func openHandles(t *testing.T, reg *turnstest.Registry) []string {
	t.Helper()
	sessions, err := reg.Sessions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, s := range sessions {
		out = append(out, s.ID)
	}
	return out
}

func TestGoneNeedsTwoCompleteReadings(t *testing.T) {
	s, reg, sc, _ := expireService(t, "term_1", "term_2")

	sc.complete(t0, "rt", "term_1")
	if got := gone(t, s, reg); len(got) != 0 {
		t.Fatalf("one reading found %v gone", got)
	}
	if got := gone(t, s, reg); len(got) != 0 {
		t.Fatalf("the same reading twice found %v gone", got)
	}
	sc.complete(t0.Add(time.Minute), "rt", "term_1")
	if got := gone(t, s, reg); len(got) != 0 {
		t.Fatalf("a second reading sooner than FastJob found %v gone", got)
	}
	sc.complete(t0.Add(s.FastJob), "rt", "term_1")
	if got := gone(t, s, reg); !slices.Equal(got, []string{"term_2"}) {
		t.Fatalf("gone %v, want term_2", got)
	}
	if len(reg.Events()) != 0 {
		t.Fatalf("a census wrote: events %+v", reg.Events())
	}
}

func TestAnIncompleteCensusMarksNothing(t *testing.T) {
	incomplete := func(sc *scripted, at time.Time) {
		sc.reading, sc.err = turns.Census{Sessions: map[string]string{}, RuntimeID: "rt", At: at}, nil
	}
	bad := []struct {
		name string
		set  func(sc *scripted, at time.Time)
	}{
		{"an incomplete census", incomplete},
		{"a failed census", func(sc *scripted, at time.Time) {
			sc.reading, sc.err = turns.Census{}, errors.New("orca is down")
		}},
	}
	for _, c := range bad {
		t.Run(c.name+" alone", func(t *testing.T) {
			s, reg, sc, _ := expireService(t, "term_1")
			for i := range 4 {
				c.set(sc, t0.Add(time.Duration(i)*s.FastJob))
				_ = s.Census(context.Background())
				if got := goneNow(t, s, reg); len(got) != 0 {
					t.Fatalf("reading %d found %v gone", i, got)
				}
			}
			if len(openHandles(t, reg)) != 1 || len(reg.Events()) != 0 {
				t.Fatalf("open %v, events %+v", openHandles(t, reg), reg.Events())
			}
		})
		t.Run(c.name+" after a complete reading", func(t *testing.T) {
			s, reg, sc, _ := expireService(t, "term_1")
			sc.complete(t0, "rt")
			gone(t, s, reg)
			c.set(sc, t0.Add(s.FastJob))
			_ = s.Census(context.Background())
			if got := goneNow(t, s, reg); len(got) != 0 {
				t.Fatalf("found %v gone from an incomplete second reading", got)
			}
			sc.complete(t0.Add(2*s.FastJob), "rt")
			if got := gone(t, s, reg); !slices.Equal(got, []string{"term_1"}) {
				t.Fatalf("a second complete reading found %v gone, want term_1", got)
			}
		})
	}
}

func TestARuntimeRestartResetsMarks(t *testing.T) {
	s, reg, sc, _ := expireService(t, "term_1")
	sc.complete(t0, "rt-a")
	gone(t, s, reg)
	sc.complete(t0.Add(s.FastJob), "rt-b")
	if got := gone(t, s, reg); len(got) != 0 {
		t.Fatalf("a census from another runtime found %v gone", got)
	}
	sc.complete(t0.Add(2*s.FastJob), "rt-b")
	if got := gone(t, s, reg); !slices.Equal(got, []string{"term_1"}) {
		t.Fatalf("gone %v, want term_1 after two readings of rt-b", got)
	}
}

func TestACensusWithNoRuntimeMarksNothing(t *testing.T) {
	s, reg, sc, _ := expireService(t, "term_1")
	for i := range 3 {
		sc.complete(t0.Add(time.Duration(i)*s.FastJob), "")
		if got := gone(t, s, reg); len(got) != 0 {
			t.Fatalf("reading %d found %v gone", i, got)
		}
	}
	sc.complete(t0.Add(3*s.FastJob), "rt")
	if got := gone(t, s, reg); len(got) != 0 {
		t.Fatalf("the first reading with a runtime found %v gone: readings with none marked", got)
	}
}

func TestAListedSessionIsNotGone(t *testing.T) {
	s, reg, sc, _ := expireService(t, "term_1")
	sc.complete(t0, "rt")
	gone(t, s, reg)
	sc.complete(t0.Add(s.FastJob), "rt", "term_1")
	if got := gone(t, s, reg); len(got) != 0 {
		t.Fatalf("a listed session is gone: %v", got)
	}
	sc.complete(t0.Add(2*s.FastJob), "rt")
	if got := gone(t, s, reg); len(got) != 0 {
		t.Fatalf("listing cleared no mark: gone %v", got)
	}
	sc.complete(t0.Add(3*s.FastJob), "rt")
	if got := gone(t, s, reg); !slices.Equal(got, []string{"term_1"}) {
		t.Fatalf("gone %v", got)
	}
}

func TestASessionStartedAfterTheCensusIsNotGone(t *testing.T) {
	s, reg, sc, _ := expireService(t)
	reg.Put(turn.Session{ID: "term_new", WorkspaceID: cleanupWS, StartedAt: t0.Add(30 * time.Minute)})
	for i := range 3 {
		sc.complete(t0.Add(time.Duration(i)*s.FastJob), "rt")
		if got := gone(t, s, reg); len(got) != 0 {
			t.Fatalf("reading %d found %v gone", i, got)
		}
	}
}

func TestCensusAtLiveSizes(t *testing.T) {
	var open, listed []string
	for i := range 159 {
		h := fmt.Sprintf("term_%03d", i)
		open = append(open, h)
		if i >= 128 {
			listed = append(listed, h)
		}
	}
	s, reg, sc, _ := expireService(t, open...)
	sc.complete(t0, "rt", listed...)
	if got := gone(t, s, reg); len(got) != 0 {
		t.Fatalf("cycle 1 found %d gone", len(got))
	}
	sc.complete(t0.Add(s.FastJob), "rt", listed...)
	if got := gone(t, s, reg); len(got) != 128 || len(openHandles(t, reg)) != 159 {
		t.Fatalf("cycle 2 found %d gone and left %d open, want 128 and 159", len(got), len(openHandles(t, reg)))
	}
}

func TestClosedSessionKeepsItsTurnWithoutACompleteCensus(t *testing.T) {
	for _, c := range []struct {
		name string
		set  func(sc *scripted)
	}{
		{"a failed census", func(sc *scripted) { sc.reading, sc.err = turns.Census{}, errors.New("orca is down") }},
		{"an incomplete census", func(sc *scripted) {
			sc.reading, sc.err = turns.Census{Sessions: map[string]string{}, RuntimeID: "rt", At: t0}, nil
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			s, store, reg, sess, _ := newService(t)
			sc := &scripted{Sessions: sess}
			s.Sessions = sc
			store.Put(started(8, turn.Intent(dintent.Review), "a", "term_1"))
			reg.Put(turn.Session{ID: "term_1", WorkspaceID: "ws1", Pane: "tab-1:leaf-1", ClosedAt: t0.Add(-time.Minute)})
			c.set(sc)
			if _, err := s.Reconcile(context.Background(), false); err != nil {
				t.Fatal(err)
			}
			if b := bookOf(t, store, ref(8)); b.Turns[0].State != turn.Started {
				t.Fatalf("turn %s, want started", b.Turns[0].State)
			}
		})
	}
}

func TestADormantSessionEndsItsOpenTurn(t *testing.T) {
	s, store, reg, sess, _ := newService(t)
	sc := &scripted{Sessions: sess}
	s.Sessions = sc
	ctx := context.Background()
	store.Put(started(8, turn.Intent(dintent.Review), "a", "term_1"))
	reg.Put(turn.Session{ID: "term_1", WorkspaceID: "ws1", Pane: "tab-1:leaf-1", StartedAt: t0.Add(-time.Hour)})

	if _, err := s.Reconcile(ctx, false); err != nil {
		t.Fatal(err)
	}
	if b := bookOf(t, store, ref(8)); b.Turns[0].State != turn.Started {
		t.Fatalf("an open session's turn is %s before the tombstone", b.Turns[0].State)
	}

	sc.complete(t0, "rt")
	gone(t, s, reg)
	sc.complete(t0.Add(s.FastJob), "rt")
	if got := gone(t, s, reg); !slices.Equal(got, []string{"term_1"}) {
		t.Fatalf("gone %v", got)
	}
	if err := s.MarkDormant(ctx, "term_1"); err != nil {
		t.Fatal(err)
	}
	rep, err := s.Reconcile(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	b := bookOf(t, store, ref(8))
	r, ok := b.LastRecord(b.Turns[0].Seq)
	if !ok || b.Turns[0].State != turn.Unrecorded || r.Record.Summary != "the session's process is gone" {
		t.Fatalf("turn %s, record %+v", b.Turns[0].State, r)
	}
	if len(rep.Unrecorded) != 1 {
		t.Fatalf("report %+v", rep)
	}
}

func TestStartupKeepsATurnWhenTheTerminalReadFails(t *testing.T) {
	for _, c := range []struct {
		name string
		set  func(sc *scripted)
	}{
		{"a census that lists the session", func(sc *scripted) { sc.complete(t0, "rt", "term_1") }},
		{"a failed census", func(sc *scripted) { sc.reading, sc.err = turns.Census{}, errors.New("orca is down") }},
		{"an incomplete census", func(sc *scripted) {
			sc.reading, sc.err = turns.Census{Sessions: map[string]string{}, RuntimeID: "rt", At: t0}, nil
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			s, store, reg, sess, _ := newService(t)
			sc := &scripted{Sessions: sess, termErr: errors.New("orca is down")}
			s.Sessions = sc
			store.Put(started(8, turn.Intent(dintent.Review), "a", "term_1"))
			reg.Put(turn.Session{ID: "term_1", WorkspaceID: "ws1", Pane: "tab-1:leaf-1"})
			c.set(sc)
			if _, err := s.Reconcile(context.Background(), true); err != nil {
				t.Fatal(err)
			}
			if b := bookOf(t, store, ref(8)); b.Turns[0].State != turn.Started {
				t.Fatalf("turn %s, want started", b.Turns[0].State)
			}
		})
	}
}
