// Package orca puts Orca's sessions and board behind the core's ports.
package orca

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/wloophq/wloop/internal/core/turns"
	"github.com/wloophq/wloop/internal/core/views"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/view"
	wsdomain "github.com/wloophq/wloop/internal/domain/workspace"
	"github.com/wloophq/wloop/internal/meter"
	"github.com/wloophq/wloop/internal/workspace"
	wsorca "github.com/wloophq/wloop/internal/workspace/orca"
)

// Columns are the board columns the configuration names.
type Columns struct {
	ReviewStatus, ReviewFallbackStatus, NeedsMeStatus, ReviewedStatus string
	InProgressStatus, InReviewStatus, ReadyStatus, CompletedStatus    string
	// AuthorColumns makes the author role own in-review, ready and completed;
	// set it with views.Service.Facts.
	AuthorColumns bool
}

// Link is a unit's workspace as its source last linked it.
type Link struct {
	WorkspaceID string
	Review      bool
}

type Adapter struct {
	m     *wsorca.Manager
	cache *workspace.Cache
	cols  Columns
	links func(context.Context) (map[unit.Unit]Link, error)

	mu     sync.Mutex
	owners map[string]cardOwner
}

type cardOwner struct {
	unit unit.Unit
	at   time.Time
}

var (
	_ turns.ManagerSessions   = (*Adapter)(nil)
	_ turns.ManagerIdleWaiter = (*Adapter)(nil)
	_ turns.ManagerRefresher  = (*Adapter)(nil)
	_ turns.ManagerLastAgents = (*Adapter)(nil)
	_ views.ViewSink          = (*Adapter)(nil)
	_ views.PlacementReader   = (*Adapter)(nil)
	_ views.StatusReader      = (*Adapter)(nil)
)

// New is an adapter whose every read asks m.
func New(m *wsorca.Manager, cfg Columns, links func(context.Context) (map[unit.Unit]Link, error)) *Adapter {
	return newAdapter(m, workspace.NewCache(m, nil, time.Now), cfg, links)
}

func newAdapter(m *wsorca.Manager, c *workspace.Cache, cfg Columns, links func(context.Context) (map[unit.Unit]Link, error)) *Adapter {
	return &Adapter{m: m, cache: c, cols: cfg, links: links, owners: map[string]cardOwner{}}
}

// Ports puts an Orca workspace manager behind the sessions and board ports. On a
// *workspace.Cached the adapter reads its cache and invalidates it on each write.
func Ports(ws workspace.Manager, cols Columns, links func(context.Context) (map[unit.Unit]Link, error)) (*Adapter, error) {
	m, ok := workspace.Direct(ws).(*wsorca.Manager)
	if !ok {
		return nil, fmt.Errorf("the Orca adapter cannot run on the workspace manager %q", ws.Name())
	}
	if s, ok := ws.(workspace.Snapshots); ok {
		return newAdapter(m, s.Snapshot(), cols, links), nil
	}
	return New(m, cols, links), nil
}

// WatchAgents calls note with the units linked to the workspaces whose agents,
// terminals or card moved, after each load of the cache that moved any, and with
// nil when the links cannot be read; the links are read under ctx.
func (a *Adapter) WatchAgents(ctx context.Context, note func(units []unit.Unit)) {
	a.cache.OnChange(func(_ *wsdomain.Snapshot, moved []string) {
		lctx, sc := meter.Start(ctx, meter.Loop("workspace"))
		defer sc.End()
		links, err := a.links(lctx)
		if err != nil {
			note(nil)
			return
		}
		var units []unit.Unit
		for u, l := range links {
			if slices.Contains(moved, l.WorkspaceID) {
				units = append(units, u)
			}
		}
		if len(units) > 0 {
			note(units)
		}
	})
}

func (a *Adapter) Processes(ctx context.Context, workspaceID string) ([]turns.Process, error) {
	s, err := a.cache.Read(ctx, wsdomain.PartTerminals)
	if err != nil {
		return nil, err
	}
	var out []turns.Process
	for _, t := range s.TerminalsOf(workspaceID) {
		if t.Usable {
			out = append(out, turns.Process{Handle: t.Handle, Key: t.TabID, Title: t.Title, Agent: t.Agent, LastOutput: t.LastOutput})
		}
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

// LastAgents are the agents of the cache's recent snapshot, without asking Orca;
// ok is false while the panes the cache holds are missing, too old or from
// another Orca runtime.
func (a *Adapter) LastAgents() (agents []turns.AgentObservation, ok bool) {
	s := a.cache.Recent(wsdomain.PartPanes)
	if s == nil {
		return nil, false
	}
	return agentsOf(s), true
}

func agentsOf(s *wsdomain.Snapshot) []turns.AgentObservation {
	var out []turns.AgentObservation
	for _, p := range s.Panes {
		if p.Agent() {
			out = append(out, turns.AgentObservation{WorkspaceID: p.WorkspaceID, Pane: p.Pane, TerminalKey: tabOf(p.Pane), State: p.State, StateSince: p.StateSince, LastMessage: p.LastMessage, UserActive: p.UserActive})
		}
	}
	return out
}

func (a *Adapter) Census(ctx context.Context) (turns.ProcessCensus, error) {
	s, err := a.cache.Read(ctx, wsdomain.PartTerminals)
	if err != nil {
		return turns.ProcessCensus{}, err
	}
	c := turns.ProcessCensus{Handles: make(map[string]string, len(s.Terminals)), Complete: s.TerminalScope.Complete,
		RuntimeID: s.TerminalScope.RuntimeID, At: s.ReadAt[wsdomain.PartTerminals]}
	for _, t := range s.Terminals {
		c.Handles[t.Handle] = t.WorkspaceID
	}
	return c, nil
}

func (a *Adapter) Refresh() { a.cache.Refresh(wsdomain.PartPanes | wsdomain.PartTerminals) }

// tabOf is the tab id an Orca pane key "<tab-id>:<leaf-id>" starts with.
func tabOf(pane string) string {
	tab, _, ok := strings.Cut(pane, ":")
	if !ok {
		return ""
	}
	return tab
}

func (a *Adapter) Type(ctx context.Context, handle, line string) (bool, error) {
	defer a.cache.Invalidate(wsdomain.PartPanes)
	return a.m.SendPrompt(ctx, handle, line, false)
}

func (a *Adapter) Close(ctx context.Context, handle string) error {
	defer a.cache.Invalidate(wsdomain.PartPanes | wsdomain.PartTerminals)
	return a.m.CloseTerminal(ctx, handle)
}

func (a *Adapter) WaitIdle(ctx context.Context, handle string, timeout time.Duration) (bool, error) {
	defer a.cache.Invalidate(wsdomain.PartPanes)
	return a.m.WaitIdle(ctx, handle, timeout)
}

func (a *Adapter) Name() string { return "orca" }

func (a *Adapter) managed(role view.Role) []string {
	switch role {
	case view.ReviewRole:
		return []string{a.cols.ReviewStatus, a.cols.ReviewFallbackStatus, a.cols.NeedsMeStatus, a.cols.ReviewedStatus, a.cols.CompletedStatus}
	case view.AuthorRole:
		if a.cols.AuthorColumns {
			return []string{a.cols.NeedsMeStatus, a.cols.InProgressStatus, a.cols.InReviewStatus, a.cols.ReadyStatus, a.cols.CompletedStatus}
		}
		return []string{a.cols.NeedsMeStatus, a.cols.InProgressStatus}
	}
	return nil
}

func (a *Adapter) Manages(role view.Role, column string) bool {
	return column != "" && slices.Contains(a.managed(role), column)
}

// Status is the status whose column on the board is column, None when the
// sink does not write that column for role.
func (a *Adapter) Status(role view.Role, column string) view.Status {
	if !a.Manages(role, column) {
		return view.None
	}
	for _, st := range []view.Status{view.NeedsMe, view.InProgress, view.InReview, view.Ready, view.Completed, view.Working, view.Reviewed, view.Done} {
		if a.column(st) == column {
			return st
		}
	}
	return view.None
}

func (a *Adapter) column(s view.Status) string {
	switch s {
	case view.Working:
		return cmp.Or(a.cols.ReviewStatus, a.cols.ReviewFallbackStatus)
	case view.NeedsMe:
		return a.cols.NeedsMeStatus
	case view.Reviewed:
		return a.cols.ReviewedStatus
	case view.Done, view.Completed:
		return a.cols.CompletedStatus
	case view.InProgress:
		return a.cols.InProgressStatus
	case view.InReview:
		return a.cols.InReviewStatus
	case view.Ready:
		return a.cols.ReadyStatus
	}
	return ""
}

// Apply writes the review items to the pull request's review workspace and
// the author items to its own one; an item without that workspace is skipped.
// A card that already shows what an item would write is not written again. The
// board is read from the cache, and read again fresh before any write so a
// card is never overwritten on a stale column.
func (a *Adapter) Apply(ctx context.Context, items []view.Item, force map[unit.Unit]bool) ([]view.Applied, error) {
	links, err := a.links(ctx)
	if err != nil {
		return nil, err
	}
	linkOf := func(u unit.Unit) (Link, bool) {
		l, ok := links[u]
		return l, ok
	}
	var eligible []view.Item
	for _, it := range items {
		if l, ok := linkOf(it.Unit); ok && it.Status != view.None && l.Review == (it.Role == view.ReviewRole) {
			eligible = append(eligible, it)
		}
	}
	items = views.NewestOwnsTheCard(eligible, func(u unit.Unit) (string, bool) {
		l, ok := linkOf(u)
		return l.WorkspaceID, ok
	})
	plans, err := a.plan(ctx, items, linkOf, force)
	if err != nil {
		return nil, err
	}
	if slices.ContainsFunc(plans, func(p cardPlan) bool { return p.writes() || force[p.item.Unit] }) {
		a.cache.Invalidate(wsdomain.PartWorkspaces)
		if plans, err = a.plan(ctx, items, linkOf, force); err != nil {
			return nil, err
		}
	}
	var out []view.Applied
	var errs []error
	for _, p := range plans {
		if p.err != nil {
			errs = append(errs, &views.ItemError{Key: p.item.Key(), Err: p.err})
			continue
		}
		if p.writes() {
			written, err := a.write(ctx, p.card, p.status, p.item)
			if err != nil {
				errs = append(errs, &views.ItemError{Key: p.item.Key(), Err: err})
				continue
			}
			p.applied.Column = written
		} else {
			p.applied.Column = p.status
		}
		a.own(p.card, p.item)
		p.applied.At = time.Now()
		out = append(out, p.applied)
	}
	return out, errors.Join(errs...)
}

// cardPlan is what Apply decided for one item from one reading of the board.
type cardPlan struct {
	item    view.Item
	card    string
	status  string
	applied view.Applied
	err     error
	same    bool
}

func (p cardPlan) writes() bool { return p.err == nil && !p.same }

func (a *Adapter) plan(ctx context.Context, items []view.Item, linkOf func(unit.Unit) (Link, bool), force map[unit.Unit]bool) ([]cardPlan, error) {
	s, err := a.cache.Read(ctx, wsdomain.PartWorkspaces)
	if err != nil {
		return nil, err
	}
	board := make(map[string]wsdomain.Workspace, len(s.Workspaces))
	for _, ws := range s.Workspaces {
		board[ws.ID] = ws
	}
	var out []cardPlan
	for _, it := range items {
		l, _ := linkOf(it.Unit)
		if a.ownedByNewer(l.WorkspaceID, it) {
			continue
		}
		ws, listed := board[l.WorkspaceID]
		status := a.column(it.Status)
		switch {
		case !listed:
			out = append(out, cardPlan{item: it, err: fmt.Errorf("workspace %s is not on the board", l.WorkspaceID)})
			continue
		case status == "":
			out = append(out, cardPlan{item: it, err: fmt.Errorf("no board column configured for %s", it.Status)})
			continue
		}
		applied := view.Applied{Unit: it.Unit, Role: it.Role, Status: it.Status, Unread: it.Unread, Summary: it.Summary}
		if !force[it.Unit] && ws.Status != "" && !a.Manages(it.Role, ws.Status) {
			status, applied.Override = "", ws.Status
		}
		out = append(out, cardPlan{item: it, card: l.WorkspaceID, status: status, applied: applied, same: unchanged(ws, status, it)})
	}
	return out, nil
}

// unchanged reports whether SetCard(status, it.Summary, it.Unread) would leave
// the card as it is: an empty field keeps the card's value, and unread is only
// ever set.
func unchanged(ws wsdomain.Workspace, status string, it view.Item) bool {
	return (status == "" || ws.Status == status) &&
		(it.Summary == "" || ws.Comment == it.Summary) &&
		(!it.Unread || ws.Unread)
}

func (a *Adapter) ownedByNewer(card string, it view.Item) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	o, ok := a.owners[card]
	return ok && o.unit != it.Unit && (o.at.After(it.At) || (o.at.Equal(it.At) && it.Unit.Less(o.unit)))
}

func (a *Adapter) own(card string, it view.Item) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.owners[card] = cardOwner{unit: it.Unit, at: it.At}
}

// write sets the card and returns the column it wrote, empty when it kept
// the card's column.
func (a *Adapter) write(ctx context.Context, id, status string, it view.Item) (string, error) {
	defer a.cache.Invalidate(wsdomain.PartWorkspaces)
	err := a.m.SetCard(ctx, id, status, it.Summary, it.Unread)
	if it.Status == view.Working && status != "" && a.cols.ReviewFallbackStatus != "" && errors.Is(err, workspace.ErrUnknownStatus) {
		return a.cols.ReviewFallbackStatus, a.m.SetCard(ctx, id, a.cols.ReviewFallbackStatus, it.Summary, it.Unread)
	}
	return status, err
}

// columns is each listed workspace's column on the board.
func (a *Adapter) columns(ctx context.Context) (map[string]string, error) {
	s, err := a.cache.Read(ctx, wsdomain.PartWorkspaces)
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(s.Workspaces))
	for _, ws := range s.Workspaces {
		out[ws.ID] = ws.Status
	}
	return out, nil
}

func (a *Adapter) Placements(ctx context.Context) (map[unit.Unit]string, error) {
	links, err := a.links(ctx)
	if err != nil {
		return nil, err
	}
	columns, err := a.columns(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[unit.Unit]string, len(links))
	for u, l := range links {
		if c, ok := columns[l.WorkspaceID]; ok {
			out[u] = c
		}
	}
	return out, nil
}
