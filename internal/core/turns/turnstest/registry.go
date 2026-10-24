package turnstest

import (
	"context"
	"maps"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/core/turns"
	"github.com/wloophq/wloop/internal/domain/turn"
)

// TestManager is the manager Put gives a session that names none.
const TestManager = "test"

// Registry is an in-memory turns.Registry.
type Registry struct {
	cfg config

	mu       sync.Mutex
	sessions map[string]turn.Session
	handles  []handleRow
	stops    []turn.Stop
	events   eventLog
}

type handleRow struct {
	id, manager, handle, runtime string
	from, to                     time.Time
	open                         bool
}

var (
	_ turns.Registry = (*Registry)(nil)
)

func NewRegistry(opts ...Option) *Registry {
	return &Registry{cfg: newConfig(opts), sessions: map[string]turn.Session{}}
}

// Put stores s without events. A session with neither manager nor handle
// that is not closed is active, its process addressed by its id under the
// manager "test".
func (r *Registry) Put(s turn.Session) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if s.Manager == "" && s.Handle == "" && s.ClosedAt.IsZero() {
		s.Manager, s.Handle = TestManager, s.ID
	}
	if s.Handle != "" {
		r.bind(s.ID, s.Manager, s.Handle, "", time.Now())
	}
	r.sessions[s.ID] = utcSession(s)
}

// Events is every event the saves wrote, in order.
func (r *Registry) Events() []port.Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.events.list)
}

func (r *Registry) Session(_ context.Context, id string) (turn.Session, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.sessions[id]
	return r.withHandle(s), ok, nil
}

func (r *Registry) withHandle(s turn.Session) turn.Session {
	s.Handle, s.LastHandle = "", ""
	var newest time.Time
	for _, h := range r.handles {
		if h.id == s.ID && !h.from.Before(newest) {
			s.LastHandle, newest = h.handle, h.from
		}
		if h.id == s.ID && h.open {
			s.Handle = h.handle
			if s.Manager == "" {
				s.Manager = h.manager
			}
		}
	}
	return s
}

func (r *Registry) SessionOf(_ context.Context, manager, handle string) (string, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var best *handleRow
	for i := range r.handles {
		h := &r.handles[i]
		if h.manager == manager && h.handle == handle && (best == nil || h.open && !best.open || h.open == best.open && h.from.After(best.from)) {
			best = h
		}
	}
	if best == nil {
		return "", false, nil
	}
	return best.id, true, nil
}

func (r *Registry) SessionOfHandle(_ context.Context, handle string) (string, string, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var id, manager string
	for _, h := range r.handles {
		if h.handle != handle {
			continue
		}
		if manager != "" && h.manager != manager {
			return "", "", false, nil
		}
		if id == "" || h.open {
			id, manager = h.id, h.manager
		}
	}
	return id, manager, manager != "", nil
}

func (r *Registry) BindHandle(_ context.Context, id, manager, handle, runtime string, at time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.bind(id, manager, handle, runtime, at)
	return nil
}

func (r *Registry) bind(id, manager, handle, runtime string, at time.Time) {
	for i := range r.handles {
		h := &r.handles[i]
		if h.id != id || !h.open {
			continue
		}
		if h.manager == manager && h.handle == handle {
			return
		}
		h.open, h.to = false, at
	}
	r.handles = append(r.handles, handleRow{id: id, manager: manager, handle: handle, runtime: runtime, from: at, open: true})
}

func (r *Registry) EndHandle(_ context.Context, id string, at time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := range r.handles {
		if h := &r.handles[i]; h.id == id && h.open {
			h.open, h.to = false, at
		}
	}
	return nil
}

func (r *Registry) SaveSession(_ context.Context, s turn.Session, evs ...port.Event) error {
	r.cfg.want(evs, "Registry.SaveSession")
	if err := checkEvents(evs); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sessions[s.ID] = utcSession(s)
	r.events.add(evs)
	return nil
}

func (r *Registry) Sessions(context.Context) ([]turn.Session, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []turn.Session
	for _, h := range slices.Sorted(maps.Keys(r.sessions)) {
		if s := r.sessions[h]; s.ClosedAt.IsZero() {
			out = append(out, r.withHandle(s))
		}
	}
	return out, nil
}

func (r *Registry) LastStop(_ context.Context, handle string) (turn.Stop, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var last turn.Stop
	found := false
	for _, s := range r.stops {
		if s.Session == handle && (!found || !s.At.Before(last.At)) {
			last, found = s, true
		}
	}
	return last, found, nil
}

func (r *Registry) LastStops(ctx context.Context, handles []string) (map[string]turn.Stop, error) {
	out := map[string]turn.Stop{}
	for _, h := range handles {
		if st, ok, _ := r.LastStop(ctx, h); ok {
			out[h] = st
		}
	}
	return out, nil
}

func (r *Registry) LogStop(_ context.Context, s turn.Stop, evs ...port.Event) error {
	r.cfg.want(evs, "Registry.LogStop")
	if err := checkEvents(evs); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	s.At = s.At.UTC().Round(0)
	r.stops = append(r.stops, s)
	r.events.add(evs)
	return nil
}

func utcSession(s turn.Session) turn.Session {
	s.StartedAt, s.MCPAt, s.IdleSince, s.ClosedAt = utc(s.StartedAt), utc(s.MCPAt), utc(s.IdleSince), utc(s.ClosedAt)
	s.TypedAt, s.UserPromptAt, s.PromptSeenAt = utc(s.TypedAt), utc(s.UserPromptAt), utc(s.PromptSeenAt)
	return s
}

func sessionEvent(handle string) port.Event {
	return port.Event{Kind: turns.KindSessionStarted, Key: handle + ":" + at0.Format(time.RFC3339Nano), At: at0}
}

// RunRegistry checks a turns.Registry; newRegistry returns an empty one.
func RunRegistry(t *testing.T, newRegistry func(t *testing.T) turns.Registry) {
	ctx := context.Background()
	save := func(t *testing.T, r turns.Registry, s turn.Session) {
		t.Helper()
		if err := r.SaveSession(ctx, s, sessionEvent(s.ID)); err != nil {
			t.Fatal(err)
		}
	}
	logStop := func(t *testing.T, r turns.Registry, s turn.Stop) {
		t.Helper()
		if err := r.LogStop(ctx, s, port.Event{Kind: turns.KindTurnStopped, Key: s.Session + ":" + s.PromptID, At: s.At}); err != nil {
			t.Fatal(err)
		}
	}

	t.Run("save session fills and overwrites every field", func(t *testing.T) {
		r := newRegistry(t)
		if _, ok, err := r.Session(ctx, "term_1"); ok || err != nil {
			t.Fatalf("an unknown handle: %v %v", ok, err)
		}
		full := turn.Session{
			ID: "term_1", Pane: "tab-1:leaf-1", AgentSession: "s-1", WorkspaceID: "repo::/wt/a", Worktree: "/wt/a", Home: "/home/me/.agent-home",
			Managed: true, StartedAt: at0, MCPAt: at0.Add(time.Second), IdleSince: at0.Add(2 * time.Second), ClosedAt: at0.Add(3 * time.Second),
		}
		save(t, r, full)
		if got, ok, err := r.Session(ctx, "term_1"); err != nil || !ok || got != full {
			t.Fatalf("got %+v %v %v\nwant %+v", got, ok, err, full)
		}
		other := turn.Session{
			ID: "term_1", Pane: "tab-2:leaf-1", AgentSession: "s-2", WorkspaceID: "repo::/wt/b", Worktree: "/wt/b", Home: "/home/me/.codex",
			StartedAt: at0.Add(time.Hour), MCPAt: at0.Add(time.Hour + time.Nanosecond),
		}
		save(t, r, other)
		if got, ok, err := r.Session(ctx, "term_1"); err != nil || !ok || got != other {
			t.Fatalf("got %+v %v %v\nwant %+v", got, ok, err, other)
		}
	})

	t.Run("sessions lists the ones not closed", func(t *testing.T) {
		r := newRegistry(t)
		save(t, r, turn.Session{ID: "term_2", StartedAt: at0})
		save(t, r, turn.Session{ID: "term_3", ClosedAt: at0})
		save(t, r, turn.Session{ID: "term_1", MCPAt: at0})
		got, err := r.Sessions(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if !slices.EqualFunc(got, []string{"term_1", "term_2"}, func(s turn.Session, h string) bool { return s.ID == h }) {
			t.Fatalf("sessions %+v", got)
		}
	})

	t.Run("last stop is the newest of the handle", func(t *testing.T) {
		r := newRegistry(t)
		if _, ok, err := turns.LastStop(ctx, r, "term_1"); ok || err != nil {
			t.Fatalf("no stop: %v %v", ok, err)
		}
		newest := turn.Stop{Session: "term_1", PromptID: "p2", AgentSession: "s-1", Message: "done", At: at0.Add(time.Minute), Blocked: true, RecordID: 4}
		logStop(t, r, newest)
		logStop(t, r, turn.Stop{Session: "term_1", PromptID: "p1", At: at0})
		logStop(t, r, turn.Stop{Session: "term_2", PromptID: "p3", At: at0.Add(time.Hour)})
		if got, ok, err := turns.LastStop(ctx, r, "term_1"); err != nil || !ok || got != newest {
			t.Fatalf("got %+v %v %v\nwant %+v", got, ok, err, newest)
		}
	})

	t.Run("last stops are the newest of each handle that logged one", func(t *testing.T) {
		r := newRegistry(t)
		newest := turn.Stop{Session: "term_1", PromptID: "p2", Message: "done", At: at0.Add(time.Minute)}
		logStop(t, r, newest)
		logStop(t, r, turn.Stop{Session: "term_1", PromptID: "p1", At: at0})
		logStop(t, r, turn.Stop{Session: "term_2", PromptID: "p3", At: at0.Add(time.Hour)})
		got, err := r.LastStops(ctx, []string{"term_1", "term_3", "term_2"})
		if err != nil || len(got) != 2 || got["term_1"] != newest || got["term_2"].PromptID != "p3" {
			t.Fatalf("got %+v %v", got, err)
		}
	})

	t.Run("log stop keeps the stops older than 30 days", func(t *testing.T) {
		r := newRegistry(t)
		logStop(t, r, turn.Stop{Session: "term_1", PromptID: "p1", At: at0})
		logStop(t, r, turn.Stop{Session: "term_3", PromptID: "p3", At: at0.Add(turns.StopsKept + time.Minute)})
		if _, ok, _ := turns.LastStop(ctx, r, "term_1"); !ok {
			t.Fatal("logging a stop dropped one older than 30 days")
		}
	})

	t.Run("a save whose events cannot be written stores nothing", func(t *testing.T) {
		r := newRegistry(t)
		if err := r.SaveSession(ctx, turn.Session{ID: "term_1"}, port.Event{Kind: turns.KindMCPSeen, Key: "term_1:x"}); err == nil {
			t.Fatal("saved an event with no time")
		}
		if _, ok, _ := r.Session(ctx, "term_1"); ok {
			t.Fatal("the session was stored")
		}
		if err := r.LogStop(ctx, turn.Stop{Session: "term_1", PromptID: "p", At: at0}, port.Event{Kind: turns.KindTurnStopped}); err == nil {
			t.Fatal("logged a stop with an event with no time")
		}
		if _, ok, _ := turns.LastStop(ctx, r, "term_1"); ok {
			t.Fatal("the stop was stored")
		}
	})

	t.Run("a session round-trips typed and prompt times", func(t *testing.T) {
		r := newRegistry(t)
		typed := turn.Session{
			ID: "term_1", StartedAt: at0, TypedAt: at0.Add(time.Minute), TypedLine: "/review https://example.test/pull/7",
			UserPromptAt: at0.Add(2*time.Minute + time.Nanosecond), PromptSeenAt: at0.Add(3 * time.Minute).In(time.FixedZone("BRT", -3*3600)),
		}
		save(t, r, typed)
		want := typed
		want.PromptSeenAt = want.PromptSeenAt.UTC()
		if got, ok, err := r.Session(ctx, "term_1"); err != nil || !ok || got != want {
			t.Fatalf("got %+v %v %v\nwant %+v", got, ok, err, want)
		}
		cleared := turn.Session{ID: "term_1", StartedAt: at0}
		save(t, r, cleared)
		if got, _, err := r.Session(ctx, "term_1"); err != nil || got != cleared {
			t.Fatalf("a save without them kept %+v %v", got, err)
		}
	})
}
