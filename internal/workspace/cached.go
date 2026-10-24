package workspace

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/wloophq/wloop/internal/domain/unit"
	wsdomain "github.com/wloophq/wloop/internal/domain/workspace"
)

// Cached is a Manager whose reads come from Cache and whose writes invalidate the parts
// they change.
type Cached struct {
	Manager
	Cache *Cache
}

// AgentIdentifier is a manager that names an agent's terminals by another identity.
type AgentIdentifier interface {
	AgentIdentity(kind string) string
}

func NewCached(m Manager, o Observer, fast time.Duration) *Cached {
	return &Cached{Manager: m, Cache: NewCache(o, DefaultMaxAge(fast), time.Now)}
}

// Snapshots is a manager whose reads come from a snapshot it keeps.
type Snapshots interface {
	Manager
	Snapshot() *Cache
	// Direct is the manager behind the snapshot, for a read that must not come from it.
	Direct() Manager
}

var _ Snapshots = (*Cached)(nil)

func (c *Cached) Snapshot() *Cache { return c.Cache }

func (c *Cached) Direct() Manager { return c.Manager }

// Invalidate marks parts stale on m when it keeps a snapshot; a no-op otherwise.
func Invalidate(m Manager, parts wsdomain.Part) {
	if s, ok := m.(Snapshots); ok {
		s.Snapshot().Invalidate(parts)
	}
}

// Direct is the manager behind m's snapshot, m itself when it keeps none.
func Direct(m Manager) Manager {
	if s, ok := m.(Snapshots); ok {
		return s.Direct()
	}
	return m
}

func (c *Cached) Reachable(ctx context.Context) error {
	if p, ok := c.Manager.(AppProbe); ok {
		if err := p.AppRunning(ctx); err != nil {
			if errors.Is(err, ErrUnreachable) || ctx.Err() != nil {
				return err
			}
			return fmt.Errorf("%w: %w", ErrUnreachable, err)
		}
	}
	if _, err := c.Cache.Read(ctx, wsdomain.PartWorkspaces); err != nil {
		if errors.Is(err, ErrUnreachable) || ctx.Err() != nil {
			return err
		}
		return fmt.Errorf("%w: %w", ErrUnreachable, err)
	}
	return nil
}

func (c *Cached) Repos(ctx context.Context) ([]wsdomain.Repo, error) {
	s, err := c.Cache.Read(ctx, wsdomain.PartRepos)
	if err != nil {
		return nil, err
	}
	return slices.Clone(s.Repos), nil
}

func (c *Cached) List(ctx context.Context) ([]wsdomain.Workspace, error) {
	s, err := c.Cache.Read(ctx, wsdomain.PartWorkspaces)
	if err != nil {
		return nil, err
	}
	return slices.Clone(s.Workspaces), nil
}

func (c *Cached) AgentActivity(ctx context.Context) (map[string]wsdomain.Activity, error) {
	s, err := c.Cache.Read(ctx, wsdomain.PartPanes)
	if err != nil {
		return nil, err
	}
	return s.Activity(), nil
}

// LiveAgentSession is the usable terminal of the agent that printed last; an empty
// kind matches any agent.
func (c *Cached) LiveAgentSession(ctx context.Context, workspaceID, agent string) (Session, bool, error) {
	s, err := c.Cache.Read(ctx, wsdomain.PartTerminals)
	if err != nil {
		return Session{}, false, err
	}
	identity := agent
	if id, ok := c.Manager.(AgentIdentifier); ok && agent != "" {
		identity = id.AgentIdentity(agent)
	}
	var best *wsdomain.Terminal
	for _, t := range s.TerminalsOf(workspaceID) {
		if t.Agent == "" || (identity != "" && t.Agent != identity) {
			continue
		}
		if best == nil || t.LastOutput.After(best.LastOutput) {
			best = &t
		}
	}
	if best == nil {
		return Session{}, false, nil
	}
	return Session{Handle: best.Handle, LastOutput: best.LastOutput}, true, nil
}

func (c *Cached) AddRepo(ctx context.Context, path string) (wsdomain.Repo, error) {
	defer c.Cache.Invalidate(wsdomain.PartRepos)
	return c.Manager.AddRepo(ctx, path)
}

func (c *Cached) Create(ctx context.Context, req CreateRequest) (wsdomain.Workspace, string, error) {
	defer c.Cache.Invalidate(wsdomain.PartMutable)
	return c.Manager.Create(ctx, req)
}

func (c *Cached) Remove(ctx context.Context, id string, force bool) error {
	defer c.Cache.Invalidate(wsdomain.PartMutable)
	return c.Manager.Remove(ctx, id, force)
}

func (c *Cached) Unregister(ctx context.Context, id string) error {
	defer c.Cache.Invalidate(wsdomain.PartMutable)
	return c.Manager.Unregister(ctx, id)
}

func (c *Cached) SetStatus(ctx context.Context, id, status string) error {
	defer c.Cache.Invalidate(wsdomain.PartWorkspaces)
	return c.Manager.(Board).SetStatus(ctx, id, status)
}

func (c *Cached) MarkUnread(ctx context.Context, id string) error {
	defer c.Cache.Invalidate(wsdomain.PartWorkspaces)
	return c.Manager.(Board).MarkUnread(ctx, id)
}

func (c *Cached) SetCard(ctx context.Context, id, status, comment string, unread bool) error {
	defer c.Cache.Invalidate(wsdomain.PartWorkspaces)
	return c.Manager.(Board).SetCard(ctx, id, status, comment, unread)
}

func (c *Cached) LinkUnit(ctx context.Context, id string, u unit.Unit) error {
	defer c.Cache.Invalidate(wsdomain.PartWorkspaces)
	return c.Manager.(Linker).LinkUnit(ctx, id, u)
}

func (c *Cached) Rename(ctx context.Context, id, displayName string) error {
	defer c.Cache.Invalidate(wsdomain.PartWorkspaces)
	return c.Manager.(Renamer).Rename(ctx, id, displayName)
}

func (c *Cached) StartAgentSession(ctx context.Context, req StartRequest) (string, error) {
	defer c.Cache.Invalidate(wsdomain.PartPanes | wsdomain.PartTerminals)
	return c.Manager.StartAgentSession(ctx, req)
}

func (c *Cached) Wake(ctx context.Context, req WakeRequest) (string, error) {
	defer c.Cache.Invalidate(wsdomain.PartPanes | wsdomain.PartTerminals)
	return c.Manager.(Waker).Wake(ctx, req)
}

func (c *Cached) CloseTerminal(ctx context.Context, handle string) error {
	defer c.Cache.Invalidate(wsdomain.PartPanes | wsdomain.PartTerminals)
	return c.Manager.(TerminalCloser).CloseTerminal(ctx, handle)
}

func (c *Cached) ReadScreen(ctx context.Context, handle string) (string, error) {
	return c.Manager.(ScreenReader).ReadScreen(ctx, handle)
}

func (c *Cached) TrustsAtLaunch(agent, home string) bool {
	t, ok := c.Manager.(LaunchTruster)
	return ok && t.TrustsAtLaunch(agent, home)
}

func (c *Cached) Honors(needs LaunchNeeds) error { return Honors(c.Manager, needs) }

func (c *Cached) ConnectsMCPOnFirstPrompt() bool {
	l, ok := c.Manager.(LazyMCP)
	return ok && l.ConnectsMCPOnFirstPrompt()
}

func (c *Cached) Closed(ctx context.Context, session string) (bool, error) {
	return c.Manager.(SessionStore).Closed(ctx, session)
}

func (c *Cached) RewritePrompt(prompt string) string {
	return c.Manager.(PromptRewriter).RewritePrompt(prompt)
}

func (c *Cached) SendPrompt(ctx context.Context, handle, prompt string, queued bool) (bool, error) {
	defer c.Cache.Invalidate(wsdomain.PartPanes)
	return c.Manager.SendPrompt(ctx, handle, prompt, queued)
}

func (c *Cached) Focus(ctx context.Context, id string) error {
	defer c.Cache.Invalidate(wsdomain.PartPanes)
	return c.Manager.Focus(ctx, id)
}
