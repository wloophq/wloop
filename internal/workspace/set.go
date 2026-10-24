package workspace

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/wloophq/wloop/internal/domain/unit"
	wsdomain "github.com/wloophq/wloop/internal/domain/workspace"
)

// ErrUnknownWorkspace is a workspace id no enabled manager lists.
var ErrUnknownWorkspace = errors.New("no enabled workspace manager lists the workspace")

// Set is every enabled workspace manager, in the configured order. It routes
// each workspace to the manager that owns it and merges what they list; it is
// not a manager itself.
type Set struct {
	ms      []Manager
	custody Custody
}

// Custody is the loop's record of which manager owns each worktree and the
// units it stands for.
type Custody interface {
	Worktrees(ctx context.Context) ([]wsdomain.Custody, error)
	Keep(ctx context.Context, c wsdomain.Custody) error
	LinkUnit(ctx context.Context, path string, u unit.Unit) error
}

// UseCustody has the set settle, from c, which manager owns a worktree that
// more than one lists, and the units each stands for.
func (s *Set) UseCustody(c Custody) { s.custody = c }

// Custody is the set's custody record, nil without one.
func (s *Set) Custody() Custody {
	if s == nil {
		return nil
	}
	return s.custody
}

func NewSet(ms ...Manager) *Set {
	return &Set{ms: slices.DeleteFunc(slices.Clone(ms), func(m Manager) bool { return m == nil })}
}

// All are the managers in the configured order.
func (s *Set) All() []Manager {
	if s == nil {
		return nil
	}
	return slices.Clone(s.ms)
}

func (s *Set) Len() int {
	if s == nil {
		return 0
	}
	return len(s.ms)
}

// First is the manager of highest priority, nil for an empty set.
func (s *Set) First() Manager {
	if s.Len() == 0 {
		return nil
	}
	return s.ms[0]
}

func (s *Set) Named(name string) (Manager, bool) {
	for _, m := range s.All() {
		if m.Name() == name {
			return m, true
		}
	}
	return nil, false
}

// Names are the managers' names in order.
func (s *Set) Names() []string {
	var out []string
	for _, m := range s.All() {
		out = append(out, m.Name())
	}
	return out
}

// Reachable is nil when at least one manager can be asked now; a manager that
// cannot only leaves its own workspaces and sessions unread.
func (s *Set) Reachable(ctx context.Context) error {
	var errs []error
	for _, m := range s.All() {
		err := m.Reachable(ctx)
		if err == nil {
			return nil
		}
		errs = append(errs, fmt.Errorf("%s: %w", m.Name(), err))
	}
	if len(errs) == 0 {
		return fmt.Errorf("%w: none is enabled", ErrUnreachable)
	}
	return errors.Join(errs...)
}

// Unreachable are the managers that cannot be asked now, with why.
func (s *Set) Unreachable(ctx context.Context) map[string]error {
	out := map[string]error{}
	for _, m := range s.All() {
		if err := m.Reachable(ctx); err != nil {
			out[m.Name()] = err
		}
	}
	return out
}

// Focus brings ws on screen in its manager's app.
func (s *Set) Focus(ctx context.Context, ws wsdomain.Workspace) error {
	m, err := s.For(ctx, ws)
	if err != nil {
		return err
	}
	return m.Focus(ctx, ws.ID)
}

// List is every manager's workspaces, each carrying its manager; a manager
// that cannot be read lists none, and only every one failing is an error.
func (s *Set) List(ctx context.Context) ([]wsdomain.Workspace, error) {
	var out []wsdomain.Workspace
	var errs []error
	for _, m := range s.All() {
		ws, err := m.List(ctx)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", m.Name(), err))
			continue
		}
		for _, w := range ws {
			w.Manager = m.Name()
			out = append(out, w)
		}
	}
	if len(errs) > 0 && len(errs) == s.Len() {
		return nil, errors.Join(errs...)
	}
	return s.settle(ctx, out, nil), nil
}

// settle keeps one workspace per worktree path, its owner's, and gives each
// the pull request its custody links when its manager shows none. The owner
// of a worktree several managers list is the one custody names, else one with
// an agent there, else the first by priority. Custody that cannot be read
// settles by priority alone.
func (s *Set) settle(ctx context.Context, ws []wsdomain.Workspace, panes []wsdomain.Pane) []wsdomain.Workspace {
	byPath := map[string]wsdomain.Custody{}
	if s.custody != nil {
		if all, err := s.custody.Worktrees(ctx); err == nil {
			for _, c := range all {
				byPath[c.Path] = c
			}
		}
	}
	rank := func(name string) int { return slices.Index(s.Names(), name) }
	active := func(w wsdomain.Workspace) bool {
		return slices.ContainsFunc(panes, func(p wsdomain.Pane) bool { return p.Manager == w.Manager && p.WorkspaceID == w.ID && p.Agent() })
	}
	owner := map[string]int{}
	for i, w := range ws {
		if w.Path == "" {
			continue
		}
		j, seen := owner[w.Path]
		if !seen {
			owner[w.Path] = i
			continue
		}
		cur := ws[j]
		c := byPath[w.Path]
		switch {
		case c.Manager == cur.Manager:
		case c.Manager == w.Manager:
			owner[w.Path] = i
		case active(cur) != active(w):
			if active(w) {
				owner[w.Path] = i
			}
		case rank(w.Manager) < rank(cur.Manager):
			owner[w.Path] = i
		}
	}
	out := make([]wsdomain.Workspace, 0, len(ws))
	for i, w := range ws {
		if w.Path != "" && owner[w.Path] != i {
			continue
		}
		if w.LinkedPR == 0 {
			for _, u := range byPath[w.Path].Units {
				if n := LinkedPR(u); n != 0 {
					w.LinkedPR = n
					break
				}
			}
		}
		out = append(out, w)
	}
	return out
}

// Repos is every manager's repositories, each carrying its manager.
func (s *Set) Repos(ctx context.Context) ([]wsdomain.Repo, error) {
	var out []wsdomain.Repo
	var errs []error
	for _, m := range s.All() {
		rs, err := m.Repos(ctx)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", m.Name(), err))
			continue
		}
		for _, r := range rs {
			r.Manager = m.Name()
			out = append(out, r)
		}
	}
	if len(errs) > 0 && len(errs) == s.Len() {
		return nil, errors.Join(errs...)
	}
	return out, nil
}

// AgentActivity is every manager's activity by workspace id.
func (s *Set) AgentActivity(ctx context.Context) (map[string]wsdomain.Activity, error) {
	out := map[string]wsdomain.Activity{}
	var errs []error
	for _, m := range s.All() {
		a, err := m.AgentActivity(ctx)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", m.Name(), err))
			continue
		}
		for id, act := range a {
			out[id] = act
		}
	}
	if len(errs) > 0 && len(errs) == s.Len() {
		return nil, errors.Join(errs...)
	}
	return out, nil
}

// For is the manager that owns ws: the one ws names, else the one that lists it.
func (s *Set) For(ctx context.Context, ws wsdomain.Workspace) (Manager, error) {
	if ws.Manager != "" {
		if m, ok := s.Named(ws.Manager); ok {
			return m, nil
		}
		return nil, fmt.Errorf("the workspace %s belongs to the workspace manager %q, which is not enabled", cmp.Or(ws.DisplayName, ws.ID), ws.Manager)
	}
	return s.Of(ctx, ws.ID)
}

// Of is the manager that lists the workspace id.
func (s *Set) Of(ctx context.Context, id string) (Manager, error) {
	if s.Len() == 1 {
		return s.ms[0], nil
	}
	for _, m := range s.All() {
		ws, err := m.List(ctx)
		if err != nil {
			continue
		}
		if slices.ContainsFunc(ws, func(w wsdomain.Workspace) bool { return w.ID == id }) {
			return m, nil
		}
	}
	return nil, fmt.Errorf("%w: %s", ErrUnknownWorkspace, id)
}

// Keep records m as the owner of the worktree ws, which m created when created.
func (s *Set) Keep(ctx context.Context, m Manager, ws wsdomain.Workspace, repo string, created bool, units ...unit.Unit) error {
	if s.custody == nil || ws.Path == "" {
		return nil
	}
	c := wsdomain.Custody{Path: ws.Path, Repo: repo, Manager: m.Name(), WorkspaceID: ws.ID, Units: units}
	if created {
		c.CreatedBy = m.Name()
	}
	return s.custody.Keep(ctx, c)
}

// LinkUnit records that the worktree ws stands for u, and has its manager
// show the link when it can.
func (s *Set) LinkUnit(ctx context.Context, ws wsdomain.Workspace, u unit.Unit) error {
	var errs []error
	if s.custody != nil && ws.Path != "" {
		m, err := s.For(ctx, ws)
		if err == nil {
			err = s.custody.Keep(ctx, wsdomain.Custody{Path: ws.Path, Manager: m.Name(), WorkspaceID: ws.ID, Units: []unit.Unit{u}})
		}
		errs = append(errs, err)
	}
	if LinkedPR(u) != 0 {
		m, err := s.For(ctx, ws)
		if err == nil {
			if l, ok := Capability[Linker](m); ok {
				err = l.LinkUnit(ctx, ws.ID, u)
			}
		}
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

// Remove deletes the workspace id under the manager that lists it, and
// forgets its custody.
func (s *Set) Remove(ctx context.Context, id string, force bool) error {
	return s.drop(ctx, id, func(m Manager) error { return m.Remove(ctx, id, force) })
}

// Unregister drops the workspace id from the manager that lists it, and
// forgets its custody.
func (s *Set) Unregister(ctx context.Context, id string) error {
	return s.drop(ctx, id, func(m Manager) error { return m.Unregister(ctx, id) })
}

func (s *Set) drop(ctx context.Context, id string, op func(Manager) error) error {
	ws, found, err := s.Find(ctx, id)
	if err != nil {
		return err
	}
	var m Manager
	if found {
		m, err = s.For(ctx, ws)
	} else {
		m, err = s.Of(ctx, id)
	}
	if err != nil {
		return err
	}
	if err := op(m); err != nil {
		return err
	}
	if d, ok := s.custody.(interface {
		Drop(ctx context.Context, path string) error
	}); ok && found && ws.Path != "" {
		return d.Drop(ctx, ws.Path)
	}
	return nil
}

// Find is the workspace id as its manager lists it.
func (s *Set) Find(ctx context.Context, id string) (wsdomain.Workspace, bool, error) {
	all, err := s.List(ctx)
	if err != nil {
		return wsdomain.Workspace{}, false, err
	}
	i := slices.IndexFunc(all, func(w wsdomain.Workspace) bool { return w.ID == id })
	if i < 0 {
		return wsdomain.Workspace{}, false, nil
	}
	return all[i], true, nil
}

// Invalidate marks parts stale on every manager that keeps a snapshot.
func (s *Set) Invalidate(parts wsdomain.Part) {
	for _, m := range s.All() {
		Invalidate(m, parts)
	}
}

// Snapshots reports whether every manager keeps a snapshot.
func (s *Set) Snapshots() bool {
	for _, m := range s.All() {
		if _, ok := m.(Snapshots); !ok {
			return false
		}
	}
	return s.Len() > 0
}

// Read merges each manager's snapshot holding want. A manager whose snapshot
// cannot be read contributes no rows and incomplete scopes; only every one
// failing is an error. Gen is the sum of the managers' generations, so it moves
// whenever one does.
func (s *Set) Read(ctx context.Context, want wsdomain.Part) (*wsdomain.Snapshot, error) {
	out := &wsdomain.Snapshot{WorkspaceScope: wsdomain.Scope{Complete: true}, TerminalScope: wsdomain.Scope{Complete: true},
		Scopes: map[string]wsdomain.Scopes{}, ReadAt: map[wsdomain.Part]time.Time{}, Loaded: want}
	var errs []error
	for _, m := range s.All() {
		sm, ok := m.(Snapshots)
		if !ok {
			continue
		}
		snap, err := sm.Snapshot().Read(ctx, want)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", m.Name(), err))
			out.Scopes[m.Name()] = wsdomain.Scopes{}
			out.WorkspaceScope.Complete, out.TerminalScope.Complete = false, false
			continue
		}
		mergeSnapshot(out, m.Name(), snap)
	}
	if len(errs) > 0 && len(errs) == s.Len() {
		return nil, errors.Join(errs...)
	}
	out.Workspaces = s.settle(ctx, out.Workspaces, out.Panes)
	return out, nil
}

func mergeSnapshot(out *wsdomain.Snapshot, name string, snap *wsdomain.Snapshot) {
	out.Gen += snap.Gen
	for _, r := range snap.Repos {
		r.Manager = name
		out.Repos = append(out.Repos, r)
	}
	for _, w := range snap.Workspaces {
		w.Manager = name
		out.Workspaces = append(out.Workspaces, w)
	}
	for _, p := range snap.Panes {
		p.Manager = name
		out.Panes = append(out.Panes, p)
	}
	for _, t := range snap.Terminals {
		t.Manager = name
		out.Terminals = append(out.Terminals, t)
	}
	out.Scopes[name] = wsdomain.Scopes{Workspaces: snap.WorkspaceScope, Terminals: snap.TerminalScope}
	out.WorkspaceScope.Complete = out.WorkspaceScope.Complete && snap.WorkspaceScope.Complete
	out.TerminalScope.Complete = out.TerminalScope.Complete && snap.TerminalScope.Complete
	out.WorkspaceScope.Hosts = append(out.WorkspaceScope.Hosts, snap.WorkspaceScope.Hosts...)
	out.TerminalScope.Hosts = append(out.TerminalScope.Hosts, snap.TerminalScope.Hosts...)
	for p, at := range snap.ReadAt {
		if at.After(out.ReadAt[p]) {
			out.ReadAt[p] = at
		}
	}
}
