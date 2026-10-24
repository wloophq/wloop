package turns_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/core/turns"
	"github.com/wloophq/wloop/internal/core/turns/turnstest"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	"github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
)

type agentSessions struct {
	mu    sync.Mutex
	saved []turns.AgentSessionInfo
}

func (a *agentSessions) SaveAgentSession(_ context.Context, info turns.AgentSessionInfo) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.saved = append(a.saved, info)
	return nil
}

func TestStopSavesTheAgentSession(t *testing.T) {
	s, _, _, _, _ := newService(t)
	as := &agentSessions{}
	s.AgentSessions = as
	ctx := context.Background()
	if err := s.SessionStarted(ctx, turns.SessionStart{Session: "term_1", AgentSession: "s1", Source: "startup", Cwd: "/wt/a", Home: "/h",
		Agent: "kind-a", Transcript: "/h/s1.log"}); err != nil {
		t.Fatal(err)
	}
	s.Clock.(*clock).advance(time.Minute)
	if _, err := s.Stop(ctx, turns.StopRequest{Session: "term_1", AgentSession: "s2", PromptID: "p", Agent: "kind-a", Transcript: "/h/s2.log"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Stop(ctx, turns.StopRequest{Session: "term_1", PromptID: "q"}); err != nil {
		t.Fatal(err)
	}
	want := []turns.AgentSessionInfo{
		{ID: "s1", Kind: "kind-a", Session: "term_1", Home: "/h", Worktree: "/wt/a", Transcript: "/h/s1.log", At: t0},
		{ID: "s2", Kind: "kind-a", Session: "term_1", Home: "/h", Worktree: "/wt/a", Transcript: "/h/s2.log", At: t0.Add(time.Minute)},
	}
	if len(as.saved) != len(want) {
		t.Fatalf("saved %+v\nwant %+v", as.saved, want)
	}
	for i := range want {
		if as.saved[i] != want[i] {
			t.Fatalf("saved[%d] %+v\nwant %+v", i, as.saved[i], want[i])
		}
	}
}

func TestStartedTurnKeepsItsAgentSession(t *testing.T) {
	s, store, _, _, _ := newService(t)
	ctx := context.Background()
	if err := s.SessionStarted(ctx, turns.SessionStart{Session: "term_1", AgentSession: "s1", Source: "startup"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Send(ctx, turns.SendRequest{Unit: r7, Intent: turn.Intent(dintent.Review), Turn: "abc", Trigger: turn.Automated, Records: true}); err != nil {
		t.Fatal(err)
	}
	if got, err := s.Start(ctx, "term_1", "abc"); err != nil || got.AgentSession != "s1" {
		t.Fatalf("started %+v %v", got, err)
	}
	if got, err := s.StartByHand(ctx, turns.SendRequest{Unit: r7, Intent: turn.Intent(dintent.Address), Turn: "def", Trigger: turn.ByHand, Records: true}, "term_1"); err != nil || got.AgentSession != "s1" {
		t.Fatalf("started by hand %+v %v", got, err)
	}
	b := bookOf(t, store, r7)
	for _, round := range []string{"abc", "def"} {
		if tu, _ := b.ByID(round); tu.AgentSession != "s1" {
			t.Fatalf("stored %s %+v", round, tu)
		}
	}
}

func TestSentTurnKeepsItsAgent(t *testing.T) {
	s, store, _, _, _ := newService(t)
	got, err := s.Send(context.Background(), turns.SendRequest{Unit: r7, Intent: turn.Intent(dintent.Review), Turn: "abc", Trigger: turn.Automated, Agent: "kind-a", Records: true})
	if err != nil || got.Agent != "kind-a" {
		t.Fatalf("sent %+v %v", got, err)
	}
	b := bookOf(t, store, r7)
	if tu, _ := b.ByID("abc"); tu.Agent != "kind-a" || tu.AgentSession != "" {
		t.Fatalf("stored %+v", tu)
	}
}

type failingAgentSessions struct{}

func (failingAgentSessions) SaveAgentSession(context.Context, turns.AgentSessionInfo) error {
	return errors.New("disk full")
}

type unreadableSessions struct{ *turnstest.Registry }

func (unreadableSessions) Session(context.Context, string) (turn.Session, bool, error) {
	return turn.Session{}, false, errors.New("locked")
}

type logs struct {
	mu    sync.Mutex
	lines []string
}

func (l *logs) logf(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lines = append(l.lines, fmt.Sprintf(format, args...))
}

func (l *logs) count(t *testing.T, n int, want string) {
	t.Helper()
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.lines) != n {
		t.Fatalf("logged %q, want %d lines", l.lines, n)
	}
	for _, line := range l.lines {
		if !strings.Contains(line, want) {
			t.Fatalf("logged %q, want %q in each line", l.lines, want)
		}
	}
}

func TestAFailedAgentSessionSaveStillDecidesTheStop(t *testing.T) {
	s, store, reg, _, _ := newService(t)
	l := &logs{}
	s.AgentSessions, s.Log = failingAgentSessions{}, l.logf
	reg.Put(turn.Session{ID: "term_1", AgentSession: "s1", StartedAt: t0.Add(-2 * time.Hour), MCPAt: t0.Add(-2 * time.Hour)})
	putTurn(store, false)
	got, err := s.Stop(context.Background(), turns.StopRequest{Session: "term_1", AgentSession: "s1", PromptID: "p1", Transcript: "/t"})
	if err != nil || !got.Block {
		t.Fatalf("stop %+v %v, want a block", got, err)
	}
	if _, logged, _ := reg.LastStop(context.Background(), "term_1"); !logged {
		t.Fatal("the stop was not logged")
	}
	l.count(t, 1, "disk full")
}

func TestAFailedAgentSessionSaveStillStartsTheSession(t *testing.T) {
	s, _, reg, _, _ := newService(t)
	l := &logs{}
	s.AgentSessions, s.Log = failingAgentSessions{}, l.logf
	ctx := context.Background()
	if err := s.SessionStarted(ctx, turns.SessionStart{Session: "term_1", AgentSession: "s1", Source: "startup"}); err != nil {
		t.Fatal(err)
	}
	if got, ok, _ := reg.Session(ctx, "term_1"); !ok || got.AgentSession != "s1" {
		t.Fatalf("session %+v %v", got, ok)
	}
	l.count(t, 1, "disk full")
}

func TestAnUnreadableSessionStartsTheTurnWithoutAnAgentSession(t *testing.T) {
	s, _, reg, _, _ := newService(t)
	l := &logs{}
	s.Registry, s.Log = unreadableSessions{reg}, l.logf
	ctx := context.Background()
	if _, err := s.Send(ctx, turns.SendRequest{Unit: r7, Intent: turn.Intent(dintent.Review), Turn: "abc", Trigger: turn.Automated, Records: true}); err != nil {
		t.Fatal(err)
	}
	if got, err := s.Start(ctx, "term_1", "abc"); err != nil || got.State != turn.Started || got.AgentSession != "" {
		t.Fatalf("started %+v %v", got, err)
	}
	if got, err := s.StartByHand(ctx, turns.SendRequest{Unit: r7, Intent: turn.Intent(dintent.Address), Turn: "def", Trigger: turn.ByHand, Records: true}, "term_2"); err != nil || got.State != turn.Started || got.AgentSession != "" {
		t.Fatalf("started by hand %+v %v", got, err)
	}
	l.count(t, 2, "locked")
}

type countingStore struct {
	turns.Store
	unitsOf, books, bookOf int
}

func (c *countingStore) UnitsOf(ctx context.Context, handles []string) (map[string]unit.Unit, error) {
	c.unitsOf++
	return c.Store.UnitsOf(ctx, handles)
}

func (c *countingStore) Books(ctx context.Context, units ...unit.Unit) (map[unit.Unit]turn.Book, error) {
	c.books++
	return c.Store.Books(ctx, units...)
}

func (c *countingStore) BookOf(ctx context.Context, k turns.Key) (turn.Book, bool, error) {
	c.bookOf++
	return c.Store.BookOf(ctx, k)
}

type countingRegistry struct {
	turns.Registry
	lastStop int
}

func (c *countingRegistry) LastStops(ctx context.Context, handles []string) (map[string]turn.Stop, error) {
	c.lastStop++
	return c.Registry.LastStops(ctx, handles)
}

func TestStopWithoutTurnSkipsBook(t *testing.T) {
	s, _, reg, _, _ := newService(t)
	store, registry, as := &countingStore{Store: s.Store}, &countingRegistry{Registry: reg}, &agentSessions{}
	s.Store, s.Registry, s.AgentSessions = store, registry, as
	ctx := context.Background()
	reg.Put(turn.Session{ID: "term_1", AgentSession: "s1", StartedAt: t0.Add(-time.Hour), MCPAt: t0.Add(-time.Hour)})
	got, err := s.Stop(ctx, turns.StopRequest{Session: "term_1", AgentSession: "s1", PromptID: "p1"})
	if err != nil || got.Block {
		t.Fatalf("stop = %+v %v", got, err)
	}
	if store.unitsOf != 1 || store.books != 0 || store.bookOf != 0 || registry.lastStop != 0 || len(as.saved) != 1 {
		t.Fatalf("UnitsOf %d Books %d BookOf %d LastStop %d agent sessions %d, want 1 0 0 0 1", store.unitsOf, store.books, store.bookOf, registry.lastStop, len(as.saved))
	}
}

func TestStopWithATurnLoadsItsBookOnce(t *testing.T) {
	s, store, reg, _, _ := newService(t)
	counted, registry := &countingStore{Store: store}, &countingRegistry{Registry: reg}
	s.Store, s.Registry = counted, registry
	reg.Put(turn.Session{ID: "term_1", AgentSession: "s1", StartedAt: t0.Add(-time.Hour), MCPAt: t0.Add(-time.Hour)})
	putTurn(store, false)
	got, err := s.Stop(context.Background(), turns.StopRequest{Session: "term_1", AgentSession: "s1", PromptID: "p1"})
	if err != nil || !got.Block {
		t.Fatalf("stop = %+v %v", got, err)
	}
	if counted.unitsOf != 1 || counted.books != 1 || counted.bookOf != 0 || registry.lastStop != 1 {
		t.Fatalf("UnitsOf %d Books %d BookOf %d LastStop %d, want 1 1 0 1", counted.unitsOf, counted.books, counted.bookOf, registry.lastStop)
	}
}
