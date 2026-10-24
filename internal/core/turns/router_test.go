package turns_test

import (
	"context"
	"errors"
	"maps"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/core/turns"
	"github.com/wloophq/wloop/internal/core/turns/turnstest"
	"github.com/wloophq/wloop/internal/domain/turn"
)

// fakeManager is one workspace manager's processes, scripted by the test.
type fakeManager struct {
	name      string
	procs     map[string][]turns.Process
	procErr   error
	agents    []turns.AgentObservation
	agentsErr error
	census    turns.ProcessCensus
	censusErr error

	mu     sync.Mutex
	typed  []string
	closed []string
}

func (m *fakeManager) Name() string { return m.name }

func (m *fakeManager) Processes(_ context.Context, workspaceID string) ([]turns.Process, error) {
	return m.procs[workspaceID], m.procErr
}

func (m *fakeManager) Agents(context.Context) ([]turns.AgentObservation, error) {
	return m.agents, m.agentsErr
}

func (m *fakeManager) Type(_ context.Context, handle, line string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.typed = append(m.typed, handle+": "+line)
	return true, nil
}

func (m *fakeManager) Census(context.Context) (turns.ProcessCensus, error) {
	return m.census, m.censusErr
}

func (m *fakeManager) Close(_ context.Context, handle string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closed = append(m.closed, handle)
	return nil
}

// newRouter routes over orca and desktop: s1 runs on orca's h1, s2 on
// desktop's d1, and s3 is dormant.
func newRouter(t *testing.T) (*turns.Router, *fakeManager, *fakeManager) {
	t.Helper()
	reg := turnstest.NewRegistry()
	reg.Put(turn.Session{ID: "s1", Manager: "orca", Handle: "h1", WorkspaceID: "w1"})
	reg.Put(turn.Session{ID: "s2", Manager: "desktop", Handle: "d1", WorkspaceID: "w2"})
	reg.Put(turn.Session{ID: "s3", Manager: "orca", WorkspaceID: "w1"})
	orca := &fakeManager{name: "orca"}
	desktop := &fakeManager{name: "desktop"}
	return turns.NewRouter(reg, orca, desktop), orca, desktop
}

func TestRouterTerminalsNameTheirSessions(t *testing.T) {
	r, orca, desktop := newRouter(t)
	orca.procs = map[string][]turns.Process{"w": {{Handle: "h1", Title: "review"}, {Handle: "stray"}}}
	desktop.procs = map[string][]turns.Process{"w": {{Handle: "d1"}, {Handle: "h1"}}}
	got, err := r.Terminals(context.Background(), "w")
	if err != nil {
		t.Fatal(err)
	}
	want := []turns.Terminal{
		{Session: "s1", Manager: "orca", Handle: "h1", Title: "review"},
		{Session: "", Manager: "orca", Handle: "stray"},
		{Session: "s2", Manager: "desktop", Handle: "d1"},
		{Session: "", Manager: "desktop", Handle: "h1"},
	}
	if !slices.Equal(got, want) {
		t.Fatalf("Terminals =\n%+v\nwant\n%+v", got, want)
	}
}

func TestRouterTerminalsKeepAReadableManager(t *testing.T) {
	r, orca, desktop := newRouter(t)
	orca.procErr = errors.New("orca down")
	desktop.procs = map[string][]turns.Process{"w": {{Handle: "d1"}}}
	got, err := r.Terminals(context.Background(), "w")
	if err != nil || len(got) != 1 || got[0].Session != "s2" {
		t.Fatalf("Terminals = %+v %v", got, err)
	}
	desktop.procErr = errors.New("desktop down")
	if _, err := r.Terminals(context.Background(), "w"); err == nil {
		t.Fatal("every manager failing is no error")
	}
}

func TestRouterTypeAndCloseRouteToTheOpenHandle(t *testing.T) {
	ctx := context.Background()
	r, orca, desktop := newRouter(t)
	if ok, err := r.Type(ctx, "s1", "one"); err != nil || !ok {
		t.Fatalf("Type s1: %v %v", ok, err)
	}
	if ok, err := r.Type(ctx, "s2", "two"); err != nil || !ok {
		t.Fatalf("Type s2: %v %v", ok, err)
	}
	if !slices.Equal(orca.typed, []string{"h1: one"}) || !slices.Equal(desktop.typed, []string{"d1: two"}) {
		t.Fatalf("typed orca %v, desktop %v", orca.typed, desktop.typed)
	}
	for _, id := range []string{"s3", "nobody"} {
		if _, err := r.Type(ctx, id, "x"); !errors.Is(err, turns.ErrDormant) {
			t.Fatalf("Type %s: %v, want ErrDormant", id, err)
		}
	}

	if err := r.Close(ctx, "s3"); err != nil {
		t.Fatalf("closing a dormant session: %v", err)
	}
	if err := r.Close(ctx, "s2"); err != nil {
		t.Fatal(err)
	}
	if len(orca.closed) != 0 || !slices.Equal(desktop.closed, []string{"d1"}) {
		t.Fatalf("closed orca %v, desktop %v", orca.closed, desktop.closed)
	}
}

func TestRouterRefusesASessionOfADisabledManager(t *testing.T) {
	reg := turnstest.NewRegistry()
	reg.Put(turn.Session{ID: "s1", Manager: "gone", Handle: "h1"})
	r := turns.NewRouter(reg, &fakeManager{name: "orca"})
	if _, err := r.Type(context.Background(), "s1", "x"); err == nil || errors.Is(err, turns.ErrDormant) {
		t.Fatalf("Type = %v, want a disabled-manager error", err)
	}
	if err := r.Close(context.Background(), "s1"); err == nil {
		t.Fatal("Close of a disabled manager's session is no error")
	}
}

func TestRouterCensusMergesManagers(t *testing.T) {
	r, orca, desktop := newRouter(t)
	orca.census = turns.ProcessCensus{Handles: map[string]string{"h1": "w1", "stray": "w1"}, Complete: true, RuntimeID: "rt-o", At: t0}
	desktop.census = turns.ProcessCensus{Handles: map[string]string{"d1": "w2"}, Complete: false, RuntimeID: "rt-d", At: t0.Add(time.Minute)}
	c, err := r.Census(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !maps.Equal(c.Sessions, map[string]string{"s1": "w1", "s2": "w2"}) {
		t.Fatalf("Sessions %v", c.Sessions)
	}
	if c.Complete || !maps.Equal(c.CompleteFor, map[string]bool{"orca": true, "desktop": false}) {
		t.Fatalf("Complete %v, CompleteFor %v", c.Complete, c.CompleteFor)
	}
	if !maps.Equal(c.RuntimeIDs, map[string]string{"orca": "rt-o", "desktop": "rt-d"}) || c.RuntimeOf("desktop") != "rt-d" {
		t.Fatalf("RuntimeIDs %v", c.RuntimeIDs)
	}
	want := map[string]turns.ProcessRef{"s1": {Manager: "orca", Handle: "h1"}, "s2": {Manager: "desktop", Handle: "d1"}}
	if !maps.Equal(c.Processes, want) {
		t.Fatalf("Processes %v", c.Processes)
	}
	if !c.At.Equal(t0.Add(time.Minute)) {
		t.Fatalf("At %v, want the latest", c.At)
	}
}

func TestRouterCensusOfAFailingManager(t *testing.T) {
	r, orca, desktop := newRouter(t)
	orca.census = turns.ProcessCensus{Handles: map[string]string{"h1": "w1"}, Complete: true, RuntimeID: "rt-o", At: t0}
	desktop.censusErr = errors.New("desktop down")
	c, err := r.Census(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if c.Complete || !c.CompleteOf("orca") || c.CompleteOf("desktop") {
		t.Fatalf("Complete %v, orca %v, desktop %v", c.Complete, c.CompleteOf("orca"), c.CompleteOf("desktop"))
	}
	if !maps.Equal(c.Sessions, map[string]string{"s1": "w1"}) {
		t.Fatalf("Sessions %v, want the readable manager's", c.Sessions)
	}
	orca.censusErr = errors.New("orca down")
	if _, err := r.Census(context.Background()); err == nil {
		t.Fatal("every manager failing is no error")
	}
}

func TestRouterAgentsTolerateAFailingManager(t *testing.T) {
	r, orca, desktop := newRouter(t)
	orca.agentsErr = errors.New("orca down")
	desktop.agents = []turns.AgentObservation{{WorkspaceID: "w2", Pane: "p"}}
	got, err := r.Agents(context.Background())
	if err != nil || len(got) != 1 || got[0].WorkspaceID != "w2" {
		t.Fatalf("Agents = %+v %v", got, err)
	}
	desktop.agentsErr = errors.New("desktop down")
	if _, err := r.Agents(context.Background()); err == nil {
		t.Fatal("every manager failing is no error")
	}
}
