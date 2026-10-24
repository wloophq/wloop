// Package turnstest holds the fakes of the turns ports and the contracts
// every implementation passes.
package turnstest

import (
	"context"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/core/turns"
	"github.com/wloophq/wloop/internal/domain/turn"
)

type Typed struct{ Handle, Line string }

// Sessions is an in-memory turns.Sessions. Type submits unless the handle is
// held or settling, or answers through OnType when set.
type Sessions struct {
	OnType func(ctx context.Context, handle, line string) (bool, error)

	mu        sync.Mutex
	terminals map[string][]turns.Terminal
	agents    []turns.AgentObservation
	held      map[string]bool
	settling  map[string]bool
	neverIdle map[string]bool
	waited    []string
	typed     []Typed
	closed    []string
	closeErr  error
	readErr   error
	partial   bool
	refreshes int
}

var (
	_ turns.Sessions   = (*Sessions)(nil)
	_ turns.IdleWaiter = (*Sessions)(nil)
	_ turns.Refresher  = (*Sessions)(nil)
)

func NewSessions() *Sessions {
	return &Sessions{terminals: map[string][]turns.Terminal{}, held: map[string]bool{}, settling: map[string]bool{}, neverIdle: map[string]bool{}}
}

// Put replaces the workspace's terminals.
func (s *Sessions) Put(workspaceID string, ts ...turns.Terminal) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.terminals[workspaceID] = slices.Clone(ts)
}

// SetAgents replaces every agent the app reports.
func (s *Sessions) SetAgents(as ...turns.AgentObservation) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.agents = slices.Clone(as)
}

// Hold makes a line typed into handle reach it without starting a turn.
func (s *Sessions) Hold(handle string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.held[handle] = true
}

// Settle makes handle an agent still ending its turn: a line typed into it
// starts no turn until WaitIdle saw its prompt.
func (s *Sessions) Settle(handle string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.settling[handle] = true
}

// NeverIdle makes WaitIdle on handle time out.
func (s *Sessions) NeverIdle(handle string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.neverIdle[handle] = true
}

// Waited is every handle WaitIdle was called on, in order.
func (s *Sessions) Waited() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.waited)
}

func (s *Sessions) WaitIdle(_ context.Context, handle string, _ time.Duration) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.waited = append(s.waited, handle)
	if s.neverIdle[handle] {
		return false, nil
	}
	delete(s.settling, handle)
	return true, nil
}

// FailReads makes Terminals, Agents and Census return err.
func (s *Sessions) FailReads(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.readErr = err
}

// FailClose makes Close return err.
func (s *Sessions) FailClose(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closeErr = err
}

// Closed is every handle Close succeeded on, in order.
func (s *Sessions) Closed() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.closed)
}

func (s *Sessions) Close(_ context.Context, handle string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closeErr != nil {
		return s.closeErr
	}
	for ws, ts := range s.terminals {
		s.terminals[ws] = slices.DeleteFunc(slices.Clone(ts), func(t turns.Terminal) bool { return t.Session == handle })
	}
	s.closed = append(s.closed, handle)
	return nil
}

func (s *Sessions) Typed() []Typed {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.typed)
}

func (s *Sessions) Terminals(_ context.Context, workspaceID string) ([]turns.Terminal, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.terminals[workspaceID]), s.readErr
}

// Census lists every terminal Put holds, complete unless Partial was called.
func (s *Sessions) Census(context.Context) (turns.Census, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := turns.Census{Sessions: map[string]string{}, Complete: !s.partial, RuntimeID: "fake"}
	for ws, ts := range s.terminals {
		for _, t := range ts {
			c.Sessions[t.Session] = ws
		}
	}
	return c, s.readErr
}

// Partial makes Census incomplete.
func (s *Sessions) Partial() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.partial = true
}

func (s *Sessions) Refresh() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.refreshes++
}

// Refreshes is how many times Refresh was called.
func (s *Sessions) Refreshes() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.refreshes
}

func (s *Sessions) Agents(context.Context) ([]turns.AgentObservation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.agents), s.readErr
}

func (s *Sessions) Type(ctx context.Context, handle, line string) (bool, error) {
	s.mu.Lock()
	s.typed = append(s.typed, Typed{Handle: handle, Line: line})
	held, on := s.held[handle] || s.settling[handle], s.OnType
	s.mu.Unlock()
	if on != nil {
		return on(ctx, handle, line)
	}
	return !held, nil
}

// SessionsWorld is the world an implementation under RunSessions stands for.
type SessionsWorld struct {
	WorkspaceID string
	Terminals   []turns.Terminal
	Agents      []turns.AgentObservation
	// Idle submits a typed line; Busy takes it without starting a turn.
	Idle, Busy string
}

var World = SessionsWorld{
	WorkspaceID: "repo::/wt/acme-api-7",
	Terminals: []turns.Terminal{
		{Session: "term_1", Key: "tab-1", Title: brand.Command + " review acme/api#7", Agent: "codex", LastOutput: time.UnixMilli(1760745600000)},
		{Session: "term_2", Key: "tab-2", Title: "zsh", LastOutput: time.UnixMilli(1760745000000)},
	},
	Agents: []turns.AgentObservation{
		{WorkspaceID: "repo::/wt/acme-api-7", Pane: "tab-1:leaf-1", TerminalKey: "tab-1", State: turn.AgentDone, LastMessage: "Published the review."},
		{WorkspaceID: "repo::/wt/acme-api-7", Pane: "tab-3:leaf-1", TerminalKey: "tab-3", State: turn.AgentWorking},
		{WorkspaceID: "repo::/wt/acme-web-9", Pane: "tab-9:leaf-2", TerminalKey: "tab-9", State: turn.AgentWaiting, UserActive: true},
	},
	Idle: "term_1",
	Busy: "term_3",
}

// NewWorldSessions is the fake seeded with World.
func NewWorldSessions() *Sessions {
	s := NewSessions()
	s.Put(World.WorkspaceID, World.Terminals...)
	s.SetAgents(World.Agents...)
	s.Hold(World.Busy)
	return s
}

// RunSessions checks a turns.Sessions that stands for World; typed returns
// every line it typed, in order.
func RunSessions(t *testing.T, newSessions func(t *testing.T) (s turns.Sessions, typed func() []Typed)) {
	ctx := context.Background()

	t.Run("terminals of a workspace", func(t *testing.T) {
		s, _ := newSessions(t)
		got, err := s.Terminals(ctx, World.WorkspaceID)
		if err != nil {
			t.Fatal(err)
		}
		if !slices.EqualFunc(got, World.Terminals, sameTerminal) {
			t.Fatalf("terminals = %+v\nwant %+v", got, World.Terminals)
		}
	})

	t.Run("agents with their pane, state and last message", func(t *testing.T) {
		s, _ := newSessions(t)
		got, err := s.Agents(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(got, World.Agents) {
			t.Fatalf("agents = %+v\nwant %+v", got, World.Agents)
		}
	})

	t.Run("census lists every terminal with its workspace", func(t *testing.T) {
		s, _ := newSessions(t)
		got, err := s.Census(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, term := range World.Terminals {
			if got.Sessions[term.Session] != World.WorkspaceID {
				t.Fatalf("census = %+v, want %s in %s", got, term.Session, World.WorkspaceID)
			}
		}
	})

	t.Run("type into an idle and a busy session", func(t *testing.T) {
		s, typed := newSessions(t)
		if ok, err := s.Type(ctx, World.Idle, "The user answered: yes"); err != nil || !ok {
			t.Fatalf("idle: submitted %v, %v", ok, err)
		}
		if ok, err := s.Type(ctx, World.Busy, "hello"); err != nil || ok {
			t.Fatalf("busy: submitted %v, %v", ok, err)
		}
		want := []Typed{{World.Idle, "The user answered: yes"}, {World.Busy, "hello"}}
		if got := typed(); !slices.Equal(got, want) {
			t.Fatalf("typed = %+v, want %+v", got, want)
		}
	})

	t.Run("close of a gone handle is not an error", func(t *testing.T) {
		s, _ := newSessions(t)
		for range 2 {
			if err := s.Close(ctx, World.Terminals[0].Session); err != nil {
				t.Fatal(err)
			}
		}
	})

	t.Run("close removes the terminal from Terminals", func(t *testing.T) {
		s, _ := newSessions(t)
		if err := s.Close(ctx, World.Terminals[0].Session); err != nil {
			t.Fatal(err)
		}
		got, err := s.Terminals(ctx, World.WorkspaceID)
		if err != nil {
			t.Fatal(err)
		}
		if !slices.EqualFunc(got, World.Terminals[1:], sameTerminal) {
			t.Fatalf("terminals after close = %+v\nwant %+v", got, World.Terminals[1:])
		}
	})
}

func sameTerminal(a, b turns.Terminal) bool {
	return a.Session == b.Session && a.Key == b.Key && a.Title == b.Title && a.Agent == b.Agent && a.LastOutput.Equal(b.LastOutput)
}

// WorldRouter is a router over one manager's sessions that stand for World,
// with every World handle taken by a session of the same id.
func WorldRouter(m interface {
	turns.ManagerSessions
	Name() string
}) turns.Sessions {
	reg := NewRegistry()
	handles := []string{World.Idle, World.Busy}
	for _, t := range World.Terminals {
		handles = append(handles, t.Session)
	}
	for _, h := range handles {
		reg.Put(turn.Session{ID: h, Manager: m.Name(), Handle: h, WorkspaceID: World.WorkspaceID})
	}
	return turns.NewRouter(reg, m)
}
