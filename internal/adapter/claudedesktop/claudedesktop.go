// Package claudedesktop puts the Claude Desktop app's sessions behind the core's ports.
package claudedesktop

import (
	"context"
	"fmt"
	"time"

	"github.com/wloophq/wloop/internal/core/turns"
	wsdomain "github.com/wloophq/wloop/internal/domain/workspace"
	"github.com/wloophq/wloop/internal/workspace"
	wscd "github.com/wloophq/wloop/internal/workspace/claudedesktop"
)

type Adapter struct {
	m     *wscd.Manager
	cache *workspace.Cache
}

var (
	_ turns.ManagerSessions   = (*Adapter)(nil)
	_ turns.ManagerIdleWaiter = (*Adapter)(nil)
	_ turns.ManagerRefresher  = (*Adapter)(nil)
	_ turns.ManagerLastAgents = (*Adapter)(nil)
)

// New is an adapter whose every read asks m.
func New(m *wscd.Manager) *Adapter {
	return &Adapter{m: m, cache: workspace.NewCache(m, nil, time.Now)}
}

// Ports puts a Claude Desktop workspace manager behind the sessions port. On a
// *workspace.Cached the adapter reads its cache and invalidates it on each write.
func Ports(ws workspace.Manager) (*Adapter, error) {
	m, ok := workspace.Direct(ws).(*wscd.Manager)
	if !ok {
		return nil, fmt.Errorf("the Claude Desktop adapter cannot run on the workspace manager %q", ws.Name())
	}
	if s, ok := ws.(workspace.Snapshots); ok {
		return &Adapter{m: m, cache: s.Snapshot()}, nil
	}
	return New(m), nil
}

func (a *Adapter) Name() string { return wscd.Name }

func (a *Adapter) Processes(ctx context.Context, workspaceID string) ([]turns.Process, error) {
	s, err := a.cache.Read(ctx, wsdomain.PartTerminals)
	if err != nil {
		return nil, err
	}
	var out []turns.Process
	for _, t := range s.TerminalsOf(workspaceID) {
		out = append(out, turns.Process{Handle: t.Handle, Key: t.TabID, Title: t.Title, Agent: t.Agent, LastOutput: t.LastOutput})
	}
	return out, nil
}

func (a *Adapter) Agents(ctx context.Context) ([]turns.AgentObservation, error) {
	s, err := a.cache.Read(ctx, wsdomain.PartPanes)
	if err != nil {
		return nil, err
	}
	return agentsOf(s), nil
}

// LastAgents are the agents of the cache's recent snapshot, without reading
// the registry; ok is false while the panes the cache holds are missing or too old.
func (a *Adapter) LastAgents() ([]turns.AgentObservation, bool) {
	s := a.cache.Recent(wsdomain.PartPanes)
	if s == nil {
		return nil, false
	}
	return agentsOf(s), true
}

// agentsOf keys each agent by its Desktop session handle, which is also the
// key of the terminal it runs in.
func agentsOf(s *wsdomain.Snapshot) []turns.AgentObservation {
	var out []turns.AgentObservation
	for _, p := range s.Panes {
		if p.Agent() {
			out = append(out, turns.AgentObservation{WorkspaceID: p.WorkspaceID, Pane: p.Pane, TerminalKey: p.Pane,
				State: p.State, StateSince: p.StateSince, LastMessage: p.LastMessage, UserActive: p.UserActive})
		}
	}
	return out
}

func (a *Adapter) Census(ctx context.Context) (turns.ProcessCensus, error) {
	s, err := a.cache.Read(ctx, wsdomain.PartTerminals)
	if err != nil {
		return turns.ProcessCensus{}, err
	}
	c := turns.ProcessCensus{Handles: make(map[string]string, len(s.Terminals)), Complete: s.TerminalsComplete(wscd.Name),
		RuntimeID: s.TerminalScope.RuntimeID, At: s.ReadAt[wsdomain.PartTerminals]}
	for _, t := range s.Terminals {
		c.Handles[t.Handle] = t.WorkspaceID
	}
	return c, nil
}

func (a *Adapter) Refresh() { a.cache.Refresh(wsdomain.PartPanes | wsdomain.PartTerminals) }

// Type delivers the line as a peer message, so a slash command goes as words.
func (a *Adapter) Type(ctx context.Context, handle, line string) (bool, error) {
	defer a.cache.Invalidate(wsdomain.PartPanes)
	return a.m.SendPrompt(ctx, handle, a.m.RewritePrompt(line), false)
}

func (a *Adapter) Close(ctx context.Context, handle string) error {
	defer a.cache.Invalidate(wsdomain.PartPanes | wsdomain.PartTerminals)
	return a.m.CloseTerminal(ctx, handle)
}

func (a *Adapter) WaitIdle(ctx context.Context, handle string, timeout time.Duration) (bool, error) {
	defer a.cache.Invalidate(wsdomain.PartPanes)
	return a.m.WaitIdle(ctx, handle, timeout)
}
