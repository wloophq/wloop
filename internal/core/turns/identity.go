package turns

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"time"

	"github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
)

// KindSessionAdopted is a process no session took becoming a session of its
// own; Key = session id, Data = manager:handle.
const KindSessionAdopted = "session_adopted"

// KindSessionDormant is a session whose process its manager no longer lists;
// Key = session id:handle.
const KindSessionDormant = "session_dormant"

// KindSessionWoken is a dormant session given a live process again; Key =
// session id:handle.
const KindSessionWoken = "session_woken"

// NewSessionID mints a session id: a random UUID, which the agent can also
// take as its own conversation id.
func NewSessionID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// handles is the registry's handle map; a registry without one is a wiring
// error that surfaces as a nil dereference in tests, never in serve.
func (s *Service) handles() Handles { return s.Registry.(Handles) }

// MintID mints a session id with the service's minter.
func (s *Service) MintID() string { return s.newID() }

func (s *Service) newID() string {
	if s.NewID != nil {
		return s.NewID()
	}
	return NewSessionID()
}

// Resolve is the session the manager's handle belongs to; a process no session
// took yet becomes a session of its own.
func (s *Service) Resolve(ctx context.Context, manager, handle string) (string, error) {
	if manager == "" || handle == "" {
		return "", errors.New("resolving a session needs its manager and handle")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.resolveLocked(ctx, manager, handle, turn.Session{})
}

func (s *Service) resolveLocked(ctx context.Context, manager, handle string, seed turn.Session) (string, error) {
	id, ok, err := s.handles().SessionOf(ctx, manager, handle)
	if err != nil || ok {
		return id, err
	}
	seed.ID, seed.Manager, seed.Handle = s.newID(), manager, ""
	ev := s.event(unit.Unit{}, KindSessionAdopted, seed.ID, manager+":"+handle)
	if err := s.Registry.SaveSession(ctx, seed, ev); err != nil {
		return "", err
	}
	if err := s.handles().BindHandle(ctx, seed.ID, manager, handle, "", s.now()); err != nil {
		return "", err
	}
	s.publish(ev)
	s.applyUnboundMCP(ctx, seed.ID, handle)
	return seed.ID, nil
}

// SessionOfHandle is the session of a handle some manager gave out, for a
// caller that knows the handle alone, such as an MCP request; ok is false for
// a handle no session took.
func (s *Service) SessionOfHandle(ctx context.Context, handle string) (string, bool, error) {
	if handle == "" {
		return "", false, nil
	}
	id, _, ok, err := s.handles().SessionOfHandle(ctx, handle)
	return id, ok, err
}

// KindSessionMerged is a session taken from a process before the loop bound
// the process to the session it started there; Key = the taken session's id,
// Data = the session it merged into.
const KindSessionMerged = "session_merged"

// Bind makes handle the live process of the session id, which a manager just
// started or woke. A session a hook or a sweep took from the same process
// before the bind merges into id: its first sightings move over and it closes.
func (s *Service) Bind(ctx context.Context, id, manager, handle, runtime string) error {
	if id == "" || manager == "" || handle == "" {
		return errors.New("binding a process needs the session, its manager and the handle")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	twin, taken, err := s.handles().SessionOf(ctx, manager, handle)
	if err != nil {
		return err
	}
	if err := s.handles().BindHandle(ctx, id, manager, handle, runtime, s.now()); err != nil {
		return err
	}
	if taken && twin != id {
		if err := s.merge(ctx, twin, id); err != nil {
			return err
		}
	}
	if f, ok := s.Sessions.(interface{ Forget(manager, handle string) }); ok {
		f.Forget(manager, handle)
	}
	s.applyUnboundMCP(ctx, id, handle)
	return nil
}

// merge closes the session twin, which took id's process first, and gives id
// what twin saw of it; it is called with mu held.
func (s *Service) merge(ctx context.Context, twin, id string) error {
	t, ok, err := s.Registry.Session(ctx, twin)
	if err != nil || !ok || !t.ClosedAt.IsZero() {
		return err
	}
	if _, err := s.applySession(ctx, id, func(cur *turn.Session, _ time.Time) (string, string) {
		if cur.MCPAt.IsZero() || t.MCPAt.After(cur.MCPAt) {
			cur.MCPAt = t.MCPAt
		}
		for _, f := range []struct {
			dst *string
			src string
		}{{&cur.AgentSession, t.AgentSession}, {&cur.Pane, t.Pane}, {&cur.Home, t.Home}, {&cur.Worktree, t.Worktree}, {&cur.WorkspaceID, t.WorkspaceID}} {
			if *f.dst == "" {
				*f.dst = f.src
			}
		}
		if cur.StartedAt.IsZero() {
			cur.StartedAt = t.StartedAt
		}
		return KindSessionMerged, twin
	}); err != nil {
		return err
	}
	if err := s.handles().EndHandle(ctx, twin, s.now()); err != nil {
		return err
	}
	t.ClosedAt, t.Handle = s.now(), ""
	ev := s.event(unit.Unit{}, KindSessionMerged, twin, id)
	if err := s.Registry.SaveSession(ctx, t, ev); err != nil {
		return err
	}
	s.publish(ev)
	return nil
}

// noteUnboundMCP keeps an MCP initialize from a handle no session took yet: a
// session the loop starts may initialize before its handle is bound.
func (s *Service) noteUnboundMCP(handle string, at time.Time) {
	if s.unboundMCP == nil {
		s.unboundMCP = map[string]time.Time{}
	}
	for h, seen := range s.unboundMCP {
		if at.Sub(seen) > unboundMCPKept {
			delete(s.unboundMCP, h)
		}
	}
	s.unboundMCP[handle] = at
}

// unboundMCPKept is how long an MCP initialize waits for its handle's bind: a
// start that has not bound by then never will, and the process belongs to no
// session the loop started.
const unboundMCPKept = 10 * time.Minute

// applyUnboundMCP gives the session the MCP initialize its handle sent before
// it was bound; it is called with mu held.
func (s *Service) applyUnboundMCP(ctx context.Context, id, handle string) {
	at, ok := s.unboundMCP[handle]
	if !ok {
		return
	}
	delete(s.unboundMCP, handle)
	if _, err := s.applySession(ctx, id, func(cur *turn.Session, _ time.Time) (string, string) {
		cur.MCPAt = at
		return KindMCPSeen, id + ":" + at.Format(time.RFC3339Nano)
	}); err != nil {
		s.logf("noting the MCP initialize of %s: %v", id, err)
	}
}

// DormantFor is the newest dormant session of u in the workspace that its
// manager could wake, with when it was last active. With ghosts, the caller
// knows the manager lists no process in the workspace, so an active session
// there is one whose process just died and counts as dormant too.
func (s *Service) DormantFor(ctx context.Context, u unit.Unit, manager, workspaceID string, ghosts bool) (turn.Session, time.Time, bool, error) {
	if u.IsZero() || workspaceID == "" {
		return turn.Session{}, time.Time{}, false, nil
	}
	all, err := s.Registry.Sessions(ctx)
	if err != nil {
		return turn.Session{}, time.Time{}, false, err
	}
	var best turn.Session
	var bestAt time.Time
	found := false
	for _, ses := range all {
		st := ses.State()
		if ses.Unit != u || ses.WorkspaceID != workspaceID || ses.Manager != manager || ses.AgentSession == "" ||
			st == turn.SessionClosed || st == turn.SessionActive && !ghosts {
			continue
		}
		at := lastActive(ses)
		if !found || at.After(bestAt) {
			best, bestAt, found = ses, at, true
		}
	}
	return best, bestAt, found, nil
}

func lastActive(ses turn.Session) time.Time {
	at := ses.StartedAt
	for _, t := range []time.Time{ses.TypedAt, ses.PromptSeenAt, ses.IdleSince} {
		if t.After(at) {
			at = t
		}
	}
	return at
}

// Woken records that the manager gave the dormant session id a live process again.
func (s *Service) Woken(ctx context.Context, id, manager, handle string) error {
	if err := s.Bind(ctx, id, manager, handle, ""); err != nil {
		return err
	}
	return s.saveSession(ctx, id, func(cur *turn.Session, now time.Time) (string, string) {
		cur.StartedAt, cur.IdleSince = now, time.Time{}
		return KindSessionWoken, id + ":" + handle
	})
}

// Placed fills the workspace, worktree and unit of the session id while it
// has none, for a session the loop took from a live process. A failure is
// logged: the session works without them.
func (s *Service) Placed(ctx context.Context, id, workspaceID, worktree string, u unit.Unit) {
	if id == "" {
		return
	}
	err := s.saveSession(ctx, id, func(cur *turn.Session, _ time.Time) (string, string) {
		if cur.WorkspaceID == "" {
			cur.WorkspaceID = workspaceID
		}
		if cur.Worktree == "" {
			cur.Worktree = worktree
		}
		if cur.Unit.IsZero() {
			cur.Unit = u
		}
		return KindSessionUnit, id
	})
	if err != nil {
		s.logf("placing the session %s: %v", id, err)
	}
}
