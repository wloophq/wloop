package workspace

import (
	"time"

	"github.com/wloophq/wloop/internal/domain/turn"
)

// Part names one listing of a Snapshot; parts combine as a bit set.
type Part uint8

const (
	PartRepos Part = 1 << iota
	PartWorkspaces
	PartPanes
	PartTerminals

	PartMutable = PartWorkspaces | PartPanes | PartTerminals
	PartAll     = PartRepos | PartMutable
)

// Scope is which hosts a listing covered. Complete is false when a host was omitted, the
// listing was truncated or the manager was not ready: absence from it proves nothing.
type Scope struct {
	Hosts    []string
	Complete bool
	// RuntimeID is the manager instance that answered; it changes on restart.
	RuntimeID string
}

// Pane is one agent the manager sees in a workspace. A Pane with no State is no agent: it
// marks the workspace open in the app while no agent runs there.
type Pane struct {
	Manager     string
	WorkspaceID string
	// Pane is "<tab-id>:<leaf-id>" for Orca; opaque to readers.
	Pane  string
	State turn.AgentState
	// StateSince is when the agent entered State; a repaint does not move it.
	StateSince  time.Time
	LastMessage string
	// UserActive means the workspace is the one open in the app.
	UserActive bool
}

// Agent reports whether p is an agent rather than the open-workspace mark.
func (p Pane) Agent() bool { return p.State != turn.AgentState{} }

// Terminal is a terminal the manager lists, usable or not.
type Terminal struct {
	Manager                                  string
	Handle, WorkspaceID, TabID, Title, Agent string
	LastOutput                               time.Time
	// Usable is connected, writable and not orphaned.
	Usable bool
}

// Snapshot is one generation of the manager's listings. Readers never modify its slices.
type Snapshot struct {
	// Gen increases by one each time a reloaded part's projection differs from the one it
	// replaces. Projections: Repos and Workspaces all fields, Panes without LastMessage,
	// Terminals without LastOutput.
	Gen            uint64
	Repos          []Repo
	Workspaces     []Workspace
	Panes          []Pane
	Terminals      []Terminal
	WorkspaceScope Scope
	TerminalScope  Scope
	// Scopes are each manager's own scopes in a snapshot merged from several;
	// nil in one manager's snapshot.
	Scopes map[string]Scopes
	// Loaded is the set of parts this snapshot holds; ReadAt is when each was read.
	Loaded Part
	ReadAt map[Part]time.Time
}

// Scopes are one manager's scopes.
type Scopes struct{ Workspaces, Terminals Scope }

// WorkspacesComplete says whether the listing holds every workspace of manager.
func (s *Snapshot) WorkspacesComplete(manager string) bool {
	if sc, ok := s.Scopes[manager]; ok {
		return sc.Workspaces.Complete
	}
	return s.WorkspaceScope.Complete
}

// TerminalsComplete says whether the listing holds every terminal of manager.
func (s *Snapshot) TerminalsComplete(manager string) bool {
	if sc, ok := s.Scopes[manager]; ok {
		return sc.Terminals.Complete
	}
	return s.TerminalScope.Complete
}

func (s *Snapshot) Workspace(id string) (Workspace, bool) {
	for _, w := range s.Workspaces {
		if w.ID == id {
			return w, true
		}
	}
	return Workspace{}, false
}

// Activity is each workspace's agents folded together, keyed by workspace id.
func (s *Snapshot) Activity() map[string]Activity {
	return ActivityOf(s.Panes)
}

// ActivityOf folds panes into the activity of each workspace they belong to.
func ActivityOf(panes []Pane) map[string]Activity {
	out := make(map[string]Activity)
	for _, p := range panes {
		a := out[p.WorkspaceID]
		a.UserActive = a.UserActive || p.UserActive
		if p.Agent() {
			a.States = append(a.States, p.State)
			switch p.State {
			case turn.AgentWorking:
				a.Busy = true
			case turn.AgentWaiting:
				a.Busy, a.Waiting = true, true
			}
		}
		out[p.WorkspaceID] = a
	}
	return out
}

// TerminalsOf is the workspace's usable terminals, in listing order.
func (s *Snapshot) TerminalsOf(workspaceID string) []Terminal {
	var out []Terminal
	for _, t := range s.Terminals {
		if t.WorkspaceID == workspaceID && t.Usable {
			out = append(out, t)
		}
	}
	return out
}

func (s *Snapshot) Terminal(handle string) (Terminal, bool) {
	for _, t := range s.Terminals {
		if t.Handle == handle {
			return t, true
		}
	}
	return Terminal{}, false
}
