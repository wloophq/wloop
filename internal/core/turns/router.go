package turns

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"sync"
	"time"
)

// Process is a live run of a session, as its manager lists it: Handle is the
// manager's address of it, never a session's identity.
type Process struct {
	// Key joins the process to the agents running in it; opaque, set by the adapter.
	Handle, Key, Title, Agent string
	LastOutput                time.Time
}

// ProcessCensus is every process handle one manager lists, with its workspace id.
type ProcessCensus struct {
	Handles map[string]string
	// Complete is false when the list may leave processes out: cut short, a host
	// left out, or the manager not ready.
	Complete bool
	// RuntimeID names the manager instance that answered; another one after a
	// restart may list other handles.
	RuntimeID string
	At        time.Time
}

// ManagerSessions is one workspace manager's processes, addressed by its
// handles; the router also asks it its manager's Name.
type ManagerSessions interface {
	Processes(ctx context.Context, workspaceID string) ([]Process, error)
	Agents(ctx context.Context) ([]AgentObservation, error)
	Type(ctx context.Context, handle, line string) (submitted bool, err error)
	Census(ctx context.Context) (ProcessCensus, error)
	// Close ends the handle's process; one already gone is not an error.
	Close(ctx context.Context, handle string) error
}

// ManagerIdleWaiter is a ManagerSessions that can wait for a process's prompt to take input again.
type ManagerIdleWaiter interface {
	WaitIdle(ctx context.Context, handle string, timeout time.Duration) (idle bool, err error)
}

// ManagerLastAgents is a ManagerSessions whose agents can be read from its last snapshot.
type ManagerLastAgents interface {
	LastAgents() (agents []AgentObservation, ok bool)
}

// ManagerRefresher is a ManagerSessions whose reads come from a snapshot.
type ManagerRefresher interface{ Refresh() }

// Handles maps the handles managers gave the processes of a session to the
// session's id.
type Handles interface {
	// SessionOf is the session whose process the manager addressed by handle.
	SessionOf(ctx context.Context, manager, handle string) (string, bool, error)
	// SessionOfHandle is SessionOf over every manager; ok is false when no
	// manager or more than one gave the handle out.
	SessionOfHandle(ctx context.Context, handle string) (id, manager string, ok bool, err error)
	// BindHandle makes handle the session's open handle, ending the one before.
	BindHandle(ctx context.Context, id, manager, handle, runtime string, at time.Time) error
	// EndHandle ends the session's open handle: the session is dormant.
	EndHandle(ctx context.Context, id string, at time.Time) error
}

// namedSessions is a manager's sessions with the manager's name.
type namedSessions interface {
	ManagerSessions
	Name() string
}

// ErrDormant is a session with no live process to type into.
var ErrDormant = errors.New("the session is dormant: it has no live process")

// Router is the sessions of every enabled manager, addressed by session id: it
// routes each call to the manager the session's open handle belongs to.
type Router struct {
	// Managers are the enabled managers in their configured order.
	Managers []namedSessions
	Registry interface {
		Session(ctx context.Context, id string) (sessionHandle, bool, error)
	}
	Handles Handles

	mu sync.Mutex
	// ids caches the session of each manager handle; a handle is never reused for
	// another session while it stays open.
	ids map[managerHandle]string
}

type managerHandle struct{ manager, handle string }

type sessionHandle struct{ Manager, Handle string }

var (
	_ Sessions   = (*Router)(nil)
	_ IdleWaiter = (*Router)(nil)
	_ LastAgents = (*Router)(nil)
	_ Refresher  = (*Router)(nil)
)

// NewRouter routes over managers, resolving handles through reg.
// NewRouter routes over managers, each a ManagerSessions with a Name, resolving
// handles through reg, which must keep them.
func NewRouter(reg Registry, managers ...namedSessions) *Router {
	h, _ := reg.(Handles)
	return &Router{Managers: managers, Registry: registryHandles{reg}, Handles: h}
}

type registryHandles struct{ r Registry }

func (r registryHandles) Session(ctx context.Context, id string) (sessionHandle, bool, error) {
	s, ok, err := r.r.Session(ctx, id)
	return sessionHandle{Manager: s.Manager, Handle: s.Handle}, ok, err
}

// With adds m, a ManagerSessions with a Name, after the router's managers.
func (r *Router) With(m namedSessions) *Router {
	r.Managers = append(r.Managers, m)
	return r
}

func (r *Router) manager(name string) (namedSessions, bool) {
	i := slices.IndexFunc(r.Managers, func(m namedSessions) bool { return m.Name() == name })
	if i < 0 {
		return nil, false
	}
	return r.Managers[i], true
}

// sessionOf is the session id of the manager's handle, "" for a handle no
// session took.
func (r *Router) sessionOf(ctx context.Context, manager, handle string) (string, error) {
	k := managerHandle{manager, handle}
	r.mu.Lock()
	id, ok := r.ids[k]
	r.mu.Unlock()
	if ok {
		return id, nil
	}
	id, found, err := r.Handles.SessionOf(ctx, manager, handle)
	if err != nil || !found {
		return "", err
	}
	r.mu.Lock()
	if r.ids == nil {
		r.ids = map[managerHandle]string{}
	}
	r.ids[k] = id
	r.mu.Unlock()
	return id, nil
}

// Forget drops the cached session of the manager's handle, for a handle bound anew.
func (r *Router) Forget(manager, handle string) {
	r.mu.Lock()
	delete(r.ids, managerHandle{manager, handle})
	r.mu.Unlock()
}

// open is the manager and the open handle of the session.
func (r *Router) open(ctx context.Context, id string) (namedSessions, string, error) {
	sh, ok, err := r.Registry.Session(ctx, id)
	if err != nil {
		return nil, "", err
	}
	if !ok || sh.Handle == "" {
		return nil, "", fmt.Errorf("%s: %w", id, ErrDormant)
	}
	m, ok := r.manager(sh.Manager)
	if !ok {
		return nil, "", fmt.Errorf("the session %s belongs to the workspace manager %q, which is not enabled", id, sh.Manager)
	}
	return m, sh.Handle, nil
}

// Terminals are the processes every manager lists in the workspace, each with
// the session that took its handle, "" for none.
func (r *Router) Terminals(ctx context.Context, workspaceID string) ([]Terminal, error) {
	var out []Terminal
	var errs []error
	for _, m := range r.Managers {
		ps, err := m.Processes(ctx, workspaceID)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", m.Name(), err))
			continue
		}
		for _, p := range ps {
			id, err := r.sessionOf(ctx, m.Name(), p.Handle)
			if err != nil {
				return nil, err
			}
			out = append(out, Terminal{Session: id, Manager: m.Name(), Handle: p.Handle, Key: p.Key, Title: p.Title, Agent: p.Agent, LastOutput: p.LastOutput})
		}
	}
	if len(out) == 0 && len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	return out, nil
}

// Agents are every reachable manager's agents; a manager that cannot be read
// lists none, and only all of them failing is an error.
func (r *Router) Agents(ctx context.Context) ([]AgentObservation, error) {
	var out []AgentObservation
	var errs []error
	for _, m := range r.Managers {
		a, err := m.Agents(ctx)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", m.Name(), err))
			continue
		}
		out = append(out, a...)
	}
	if len(errs) == len(r.Managers) && len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	return out, nil
}

func (r *Router) LastAgents() ([]AgentObservation, bool) {
	var out []AgentObservation
	for _, m := range r.Managers {
		l, ok := m.(ManagerLastAgents)
		if !ok {
			return nil, false
		}
		a, ok := l.LastAgents()
		if !ok {
			return nil, false
		}
		out = append(out, a...)
	}
	return out, true
}

func (r *Router) Refresh() {
	for _, m := range r.Managers {
		if f, ok := m.(ManagerRefresher); ok {
			f.Refresh()
		}
	}
}

func (r *Router) Type(ctx context.Context, id, line string) (bool, error) {
	m, h, err := r.open(ctx, id)
	if err != nil {
		return false, err
	}
	return m.Type(ctx, h, line)
}

func (r *Router) Close(ctx context.Context, id string) error {
	m, h, err := r.open(ctx, id)
	if errors.Is(err, ErrDormant) {
		return nil
	}
	if err != nil {
		return err
	}
	return m.Close(ctx, h)
}

func (r *Router) WaitIdle(ctx context.Context, id string, timeout time.Duration) (bool, error) {
	m, h, err := r.open(ctx, id)
	if err != nil {
		return false, err
	}
	w, ok := m.(ManagerIdleWaiter)
	if !ok {
		return false, fmt.Errorf("the workspace manager %q cannot wait for an idle agent", m.Name())
	}
	return w.WaitIdle(ctx, h, timeout)
}

// Census is every manager's census, keyed by the session that took each
// handle; a manager that cannot be read counts as incomplete, never as
// listing no session.
func (r *Router) Census(ctx context.Context) (Census, error) {
	c := Census{Sessions: map[string]string{}, Complete: true, CompleteFor: map[string]bool{}, RuntimeIDs: map[string]string{}, Processes: map[string]ProcessRef{}}
	var errs []error
	for _, m := range r.Managers {
		pc, err := m.Census(ctx)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", m.Name(), err))
			c.Complete = false
			continue
		}
		c.CompleteFor[m.Name()] = pc.Complete
		c.RuntimeIDs[m.Name()] = pc.RuntimeID
		c.Complete = c.Complete && pc.Complete
		if pc.At.After(c.At) {
			c.At = pc.At
		}
		for _, h := range slices.Sorted(maps.Keys(pc.Handles)) {
			id, err := r.sessionOf(ctx, m.Name(), h)
			if err != nil {
				return Census{}, err
			}
			if id != "" {
				c.Sessions[id] = pc.Handles[h]
				c.Processes[id] = ProcessRef{Manager: m.Name(), Handle: h}
			}
		}
	}
	if len(errs) == len(r.Managers) && len(errs) > 0 {
		return Census{}, errors.Join(errs...)
	}
	return c, nil
}
