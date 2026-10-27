// Package fake is an in-memory workspace.Manager for tests.
package fake

import (
	"context"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
	wsdomain "github.com/wloophq/wloop/internal/domain/workspace"
	"github.com/wloophq/wloop/internal/meter"
	"github.com/wloophq/wloop/internal/workspace"
)

type Manager struct {
	mu sync.Mutex

	RepoList      []wsdomain.Repo
	WorkspaceList []wsdomain.Workspace
	Activity      map[string]wsdomain.Activity
	// Sessions maps a workspace id to its live agent terminal handle.
	Sessions map[string]string
	// LastOutput is when each live session last printed; zero means long ago.
	LastOutput map[string]time.Time
	// Commands records the command of each agent session started.
	Commands []string
	// Columns lists the statuses SetStatus accepts; nil accepts any.
	Columns []string
	// NotSubmitted makes SendPrompt report the prompt as not observed.
	NotSubmitted bool
	// BranchSuffix is appended to the head branch of a created workspace.
	// Create also appends "-2", as Orca does, when a workspace of the
	// repository has the branch checked out or Tips holds it away from the
	// PR head.
	BranchSuffix string
	// Tips is the commit of each local branch; Create puts the new branch at
	// the PR head.
	Tips map[string]string
	// Creates records each create request. A parent no listed workspace has
	// the path of is dropped, as Orca drops it.
	Creates []workspace.CreateRequest
	// StartFails and SendFails fail StartAgentSession and SendPrompt.
	StartFails, SendFails error
	// WakeLate fails Wake after it started the process, which it names.
	WakeLate error
	// Blocked makes a started session wait on this prompt.
	Blocked     string
	Unreachable error
	// Screen is what every terminal renders.
	Screen string
	// TrustingHomes are the agent homes TrustsAtLaunch matches.
	TrustingHomes []string
	// WorktreeDir, when set, is where Create makes each workspace's folder,
	// which Remove deletes; otherwise paths are under /worktrees and no
	// folder exists.
	WorktreeDir string
	// ManagerName is what Name reports, "fake" when empty.
	ManagerName string
	// OnlyAgents are the agents Hosts accepts; nil hosts every agent.
	OnlyAgents []string
	// Refuses is what Honors returns for every launch policy that asks more than defaults.
	Refuses error
	// Wakes records each session Wake resumed.
	Wakes []workspace.WakeRequest

	Writes  []string
	Prompts []Prompt
}

type Prompt struct{ Handle, Text string }

var (
	_ workspace.Manager  = (*Manager)(nil)
	_ workspace.Observer = (*Manager)(nil)
)

// sample records one Workspace call that spawns nothing.
func sample(ctx context.Context, op string, err error) {
	meter.Add(ctx, meter.Sample{Res: meter.Workspace, Op: op, Err: err != nil && ctx.Err() == nil})
}

func (m *Manager) write(format string, args ...any) {
	m.Writes = append(m.Writes, fmt.Sprintf(format, args...))
}

func (m *Manager) Name() string {
	if m.ManagerName == "" {
		return "fake"
	}
	return m.ManagerName
}

func (m *Manager) Hosts(agent string) bool {
	return m.OnlyAgents == nil || slices.Contains(m.OnlyAgents, agent)
}

func (m *Manager) Honors(needs workspace.LaunchNeeds) error {
	if needs == (workspace.LaunchNeeds{}) {
		return nil
	}
	return m.Refuses
}

func (m *Manager) Wake(ctx context.Context, req workspace.WakeRequest) (_ string, err error) {
	defer func() { sample(ctx, "Wake", err) }()
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.StartFails != nil {
		return "", m.StartFails
	}
	m.write("wake %s", req.Session)
	m.Wakes = append(m.Wakes, req)
	if m.Sessions == nil {
		m.Sessions = map[string]string{}
	}
	h := "term-" + req.WorkspaceID
	m.Sessions[req.WorkspaceID] = h
	return h, m.WakeLate
}

func (m *Manager) SetCard(ctx context.Context, id, status, comment string, unread bool) error {
	if status != "" {
		if err := m.SetStatus(ctx, id, status); err != nil {
			return err
		}
	}
	if unread {
		return m.MarkUnread(ctx, id)
	}
	return nil
}

func (m *Manager) Reachable(ctx context.Context) error {
	sample(ctx, "Reachable", m.Unreachable)
	return m.Unreachable
}

func (m *Manager) Repos(ctx context.Context) (_ []wsdomain.Repo, err error) {
	defer func() { sample(ctx, "Repos", err) }()
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Unreachable != nil {
		return nil, m.Unreachable
	}
	return slices.Clone(m.RepoList), nil
}

func (m *Manager) AddRepo(ctx context.Context, path string) (_ wsdomain.Repo, err error) {
	defer func() { sample(ctx, "AddRepo", err) }()
	m.mu.Lock()
	defer m.mu.Unlock()
	m.write("repo add %s", path)
	r := wsdomain.Repo{ID: fmt.Sprintf("repo-%d", len(m.RepoList)+1), Path: path}
	m.RepoList = append(m.RepoList, r)
	return r, nil
}

func (m *Manager) List(ctx context.Context) (_ []wsdomain.Workspace, err error) {
	defer func() { sample(ctx, "List", err) }()
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Unreachable != nil {
		return nil, m.Unreachable
	}
	return slices.Clone(m.WorkspaceList), nil
}

func (m *Manager) Create(ctx context.Context, req workspace.CreateRequest) (_ wsdomain.Workspace, _ string, err error) {
	defer func() { sample(ctx, "Create", err) }()
	m.mu.Lock()
	defer m.mu.Unlock()
	m.write("create %s", req.Name)
	m.Creates = append(m.Creates, req)
	w := wsdomain.Workspace{
		ID:          fmt.Sprintf("ws-%d", len(m.WorkspaceList)+1),
		RepoID:      req.RepoID,
		Path:        "/worktrees/" + req.Name,
		DisplayName: req.Name,
		HostID:      wsdomain.LocalHost,
		LinkedPR:    workspace.LinkedPR(req.Unit),
	}
	if m.WorktreeDir != "" {
		w.Path = filepath.Join(m.WorktreeDir, req.Name)
		if err := os.MkdirAll(w.Path, 0o755); err != nil {
			return wsdomain.Workspace{}, "", err
		}
	}
	if req.HeadBranch != "" {
		w.Branch = req.HeadBranch + m.BranchSuffix
		checkedOut := slices.ContainsFunc(m.WorkspaceList, func(o wsdomain.Workspace) bool { return o.RepoID == req.RepoID && o.Branch == w.Branch })
		if tip := m.Tips[w.Branch]; checkedOut || (tip != "" && tip != req.HeadOid) {
			w.Branch += "-2"
		} else if m.Tips != nil && req.HeadOid != "" {
			m.Tips[w.Branch] = req.HeadOid
		}
		w.BranchRenamed = w.Branch != req.HeadBranch
	}
	w.ParentDropped = req.Parent != "" && !slices.ContainsFunc(m.WorkspaceList, func(o wsdomain.Workspace) bool { return o.Path == req.Parent })
	m.WorkspaceList = append(m.WorkspaceList, w)
	handle := "term-" + w.ID
	if req.Agent != "" {
		m.Prompts = append(m.Prompts, Prompt{handle, req.Prompt})
	}
	return w, handle, nil
}

func (m *Manager) SetStatus(ctx context.Context, id, status string) (err error) {
	defer func() { sample(ctx, "SetStatus", err) }()
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Columns != nil && !slices.Contains(m.Columns, status) {
		return fmt.Errorf("%w %q", workspace.ErrUnknownStatus, status)
	}
	m.write("status %s %s", id, status)
	for i := range m.WorkspaceList {
		if m.WorkspaceList[i].ID == id {
			m.WorkspaceList[i].Status = status
		}
	}
	return nil
}

func (m *Manager) MarkUnread(ctx context.Context, id string) (err error) {
	defer func() { sample(ctx, "MarkUnread", err) }()
	m.mu.Lock()
	defer m.mu.Unlock()
	m.write("unread %s", id)
	return nil
}

// LinkUnit records "link <id> <number>" for a pull request, as Orca shows
// one; it shows no other unit.
func (m *Manager) LinkUnit(ctx context.Context, id string, u unit.Unit) (err error) {
	defer func() { sample(ctx, "LinkUnit", err) }()
	m.mu.Lock()
	defer m.mu.Unlock()
	if n := workspace.LinkedPR(u); n != 0 {
		m.write("link %s %d", id, n)
	}
	return nil
}

func (m *Manager) Rename(ctx context.Context, id, displayName string) (err error) {
	defer func() { sample(ctx, "Rename", err) }()
	m.mu.Lock()
	defer m.mu.Unlock()
	m.write("rename %s %s", id, displayName)
	for i := range m.WorkspaceList {
		if m.WorkspaceList[i].ID == id {
			m.WorkspaceList[i].DisplayName = displayName
		}
	}
	return nil
}

func (m *Manager) Workspaces(ctx context.Context) (_ []wsdomain.Workspace, _ wsdomain.Scope, err error) {
	defer func() { sample(ctx, "Workspaces", err) }()
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Unreachable != nil {
		return nil, wsdomain.Scope{}, m.Unreachable
	}
	return slices.Clone(m.WorkspaceList), m.scope(), nil
}

// Panes lists one pane per state in Activity. An entry without States gives one pane
// that keeps its Waiting or Busy, else a bare pane when the workspace is open.
func (m *Manager) Panes(ctx context.Context) (_ []wsdomain.Pane, err error) {
	defer func() { sample(ctx, "Panes", err) }()
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Unreachable != nil {
		return nil, m.Unreachable
	}
	var out []wsdomain.Pane
	for _, id := range slices.Sorted(maps.Keys(m.Activity)) {
		a := m.Activity[id]
		if len(a.States) == 0 {
			p := wsdomain.Pane{WorkspaceID: id, Pane: fmt.Sprintf("tab-%s:leaf-1", id), UserActive: a.UserActive}
			switch {
			case a.Waiting:
				p.State = turn.AgentWaiting
			case a.Busy:
				p.State = turn.AgentWorking
			case a.UserActive:
				p.Pane = ""
			default:
				continue
			}
			out = append(out, p)
		}
		for i, state := range a.States {
			out = append(out, wsdomain.Pane{WorkspaceID: id, Pane: fmt.Sprintf("tab-%s:leaf-%d", id, i+1), State: state, UserActive: a.UserActive})
		}
	}
	return out, nil
}

// AllTerminals lists the live session of each workspace in Sessions.
func (m *Manager) AllTerminals(ctx context.Context) (_ []wsdomain.Terminal, _ wsdomain.Scope, err error) {
	defer func() { sample(ctx, "AllTerminals", err) }()
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Unreachable != nil {
		return nil, wsdomain.Scope{}, m.Unreachable
	}
	var out []wsdomain.Terminal
	for _, id := range slices.Sorted(maps.Keys(m.Sessions)) {
		out = append(out, wsdomain.Terminal{Handle: m.Sessions[id], WorkspaceID: id, LastOutput: m.LastOutput[id], Usable: true})
	}
	return out, m.scope(), nil
}

func (m *Manager) scope() wsdomain.Scope {
	return wsdomain.Scope{Hosts: []string{wsdomain.LocalHost}, Complete: true, RuntimeID: "fake"}
}

func (m *Manager) AgentActivity(ctx context.Context) (_ map[string]wsdomain.Activity, err error) {
	defer func() { sample(ctx, "AgentActivity", err) }()
	m.mu.Lock()
	defer m.mu.Unlock()
	return maps.Clone(m.Activity), nil
}

func (m *Manager) LiveAgentSession(ctx context.Context, workspaceID, _ string) (_ workspace.Session, _ bool, err error) {
	defer func() { sample(ctx, "LiveAgentSession", err) }()
	m.mu.Lock()
	defer m.mu.Unlock()
	h, ok := m.Sessions[workspaceID]
	return workspace.Session{Handle: h, LastOutput: m.LastOutput[workspaceID]}, ok, nil
}

func (m *Manager) StartAgentSession(ctx context.Context, req workspace.StartRequest) (_ string, err error) {
	defer func() { sample(ctx, "StartAgentSession", err) }()
	workspaceID, command := req.WorkspaceID, req.Command
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.StartFails != nil {
		return "", m.StartFails
	}
	m.write("session %s", workspaceID)
	m.Commands = append(m.Commands, command)
	if m.Sessions == nil {
		m.Sessions = map[string]string{}
	}
	h := "term-" + workspaceID
	m.Sessions[workspaceID] = h
	if m.Blocked != "" {
		return "", &workspace.BlockedError{Handle: h, Reason: m.Blocked}
	}
	return h, nil
}

func (m *Manager) CloseTerminal(ctx context.Context, handle string) (err error) {
	defer func() { sample(ctx, "CloseTerminal", err) }()
	m.mu.Lock()
	defer m.mu.Unlock()
	m.write("close %s", handle)
	maps.DeleteFunc(m.Sessions, func(_, h string) bool { return h == handle })
	return nil
}

func (m *Manager) ReadScreen(ctx context.Context, _ string) (_ string, err error) {
	defer func() { sample(ctx, "ReadScreen", err) }()
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.Screen, nil
}

func (m *Manager) SendPrompt(ctx context.Context, handle, prompt string, queued bool) (_ bool, err error) {
	defer func() { sample(ctx, "SendPrompt", err) }()
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.SendFails != nil {
		return false, m.SendFails
	}
	if queued {
		m.write("queue %s", handle)
	} else {
		m.write("send %s", handle)
	}
	m.Prompts = append(m.Prompts, Prompt{handle, prompt})
	return !m.NotSubmitted, nil
}

func (m *Manager) Focus(ctx context.Context, id string) (err error) {
	defer func() { sample(ctx, "Focus", err) }()
	m.mu.Lock()
	defer m.mu.Unlock()
	m.write("focus %s", id)
	return nil
}

func (m *Manager) Remove(ctx context.Context, id string, force bool) (err error) {
	defer func() { sample(ctx, "Remove", err) }()
	m.mu.Lock()
	defer m.mu.Unlock()
	if force {
		m.write("remove --force %s", id)
	} else {
		m.write("remove %s", id)
	}
	m.WorkspaceList = slices.DeleteFunc(m.WorkspaceList, func(w wsdomain.Workspace) bool {
		if w.ID == id && m.WorktreeDir != "" && strings.HasPrefix(w.Path, m.WorktreeDir) {
			os.RemoveAll(w.Path)
		}
		return w.ID == id
	})
	return nil
}

func (m *Manager) Unregister(ctx context.Context, id string) (err error) {
	defer func() { sample(ctx, "Unregister", err) }()
	m.mu.Lock()
	defer m.mu.Unlock()
	m.write("unregister %s", id)
	m.WorkspaceList = slices.DeleteFunc(m.WorkspaceList, func(w wsdomain.Workspace) bool { return w.ID == id })
	return nil
}

func (m *Manager) TrustsAtLaunch(_, home string) bool { return slices.Contains(m.TrustingHomes, home) }
