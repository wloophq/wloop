// Package factworkspace provides the workflow facts of the workspaces the
// workspace manager lists, from its snapshot, the stored pull requests and
// local git reads that reach no network.
package factworkspace

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/wloophq/wloop/internal/config"
	"github.com/wloophq/wloop/internal/core/automation"
	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/core/sweep"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/workflow"
	"github.com/wloophq/wloop/internal/domain/workspace"
	"github.com/wloophq/wloop/internal/meter"
)

// GainWalk bounds the walk that sizes a workspace for its gain.
const GainWalk = 10 * time.Second

// Scope is the meter scope the provider's reads run under, nested in the
// caller's.
const Scope = "facts:workspace"

var (
	_ automation.Units  = (*Provider)(nil)
	_ automation.Gainer = (*Provider)(nil)
	_ automation.Seener = (*Provider)(nil)
	_ automation.Dueer  = (*Provider)(nil)
)

// Rows are the stored pull requests, open or not, and the user's login on
// their provider ("" when not known yet).
type Rows interface {
	Rows(ctx context.Context) ([]dinv.PR, error)
	Viewer(ctx context.Context) (string, error)
}

// Overrides are the user's overrides, read for a taken-over pull request.
type Overrides interface {
	Overrides(ctx context.Context) (map[unit.Unit]dinv.Override, error)
}

// Git reads a checkout without reaching the network or taking a lock
// another git process would wait on.
type Git interface {
	Head(ctx context.Context, dir string) (string, error)
	// Status counts the changed tracked files and lists the untracked ones.
	Status(ctx context.Context, dir string) (dirty int, untracked []string, err error)
	// Unpushed counts the commits of HEAD that no remote-tracking ref holds.
	Unpushed(ctx context.Context, dir string) (int, error)
}

// Disk is what the provider reads of a workspace's directory. Touched and
// Index are file modification times: a fixture sets them with os.Chtimes or
// passes factworkspacetest.Disk.
type Disk interface {
	Exists(path string) bool
	// Touched is when git last moved the checkout's HEAD; zero when unknown.
	Touched(path string) time.Time
	// Index is when the checkout's index last changed; zero when unknown.
	Index(path string) time.Time
	// Size is the bytes under path, as far as it got before ctx ended.
	Size(ctx context.Context, path string) (int64, error)
}

// Generations is the change log's generation.
type Generations interface {
	Gen() port.Generation
}

// Sessions are the open agent sessions, active or dormant.
type Sessions interface {
	Sessions(ctx context.Context) ([]turn.Session, error)
}

type Deps struct {
	Snapshot sweep.Snapshot
	// Sessions is nil when no turns are kept: every workspace then has none.
	Sessions  Sessions
	PRs       Rows
	Overrides Overrides
	Books     sweep.Books
	Git       Git
	Disk      Disk
	Changes   Generations
	Cfg       func() config.Config
	Clock     port.Clock
}

// Provider is the facts provider of unit provider "workspace".
type Provider struct {
	d Deps

	mu   sync.Mutex
	seen port.Generation
	due  map[string][]time.Time
	ix   *index
	ixAt indexKey
	c    *reads
}

// reads are the checkout reads siblings share: the local git reads and the
// gain walks.
type reads struct {
	mu    sync.Mutex
	gain  map[string]gained
	local map[string]cachedLocal
}

type gained struct {
	fingerprint string
	gain        workflow.Gain
}

// localKey is what moves when a checkout's local facts may have changed.
type localKey struct {
	head                     string
	index, touched, activity time.Time
}

type cachedLocal struct {
	key  localKey
	at   time.Time
	read local
}

type indexKey struct {
	snap uint64
	gen  port.Generation
}

func New(d Deps) *Provider {
	return &Provider{d: d, due: map[string][]time.Time{}, c: &reads{gain: map[string]gained{}, local: map[string]cachedLocal{}}}
}

// Sibling is a provider over the same deps and checkout reads, with its
// own Seen and deadlines: a plan's listing never moves the sweep's Seen.
func (p *Provider) Sibling() *Provider {
	return &Provider{d: p.d, due: map[string][]time.Time{}, c: p.c}
}

func (p *Provider) read(ctx context.Context) (*workspace.Snapshot, error) {
	return p.d.Snapshot.Read(ctx, workspace.PartAll)
}

// List is every workspace of the managers whose listing is complete, none
// of a manager whose listing is not.
func (p *Provider) List(ctx context.Context, prov unit.ProviderID) ([]unit.Unit, error) {
	ctx, sc := meter.Start(ctx, Scope)
	defer sc.End()
	if prov != unit.Workspace {
		return nil, fmt.Errorf("the workspace facts provider lists no %s units", prov)
	}
	snap, err := p.read(ctx)
	if err != nil {
		return nil, err
	}
	p.mu.Lock()
	if p.d.Changes != nil {
		p.seen = p.d.Changes.Gen()
	}
	listed := map[string]bool{}
	for _, w := range snap.Workspaces {
		listed[w.Path] = true
	}
	for id := range p.due {
		if !listed[id] {
			delete(p.due, id)
		}
	}
	p.mu.Unlock()
	p.c.mu.Lock()
	for id := range p.c.local {
		if !listed[id] {
			delete(p.c.local, id)
		}
	}
	p.c.mu.Unlock()
	out := make([]unit.Unit, 0, len(snap.Workspaces))
	for _, w := range snap.Workspaces {
		if snap.WorkspacesComplete(w.Manager) {
			out = append(out, workspace.UnitOf(w.Path))
		}
	}
	slices.SortFunc(out, func(a, b unit.Unit) int { return strings.Compare(a.Key, b.Key) })
	return slices.CompactFunc(out, func(a, b unit.Unit) bool { return a == b }), nil
}

// Facts are the workspace slot of u's facts.
func (p *Provider) Facts(ctx context.Context, u unit.Unit) (workflow.Facts, error) {
	ctx, sc := meter.Start(ctx, Scope)
	defer sc.End()
	snap, w, err := p.workspace(ctx, u)
	if err != nil {
		return nil, err
	}
	cfg := p.d.Cfg()
	ix, err := p.index(ctx, snap, cfg)
	if err != nil {
		return nil, err
	}
	f, deadlines, err := p.facts(ctx, snap, ix, w, cfg)
	if err != nil {
		return nil, err
	}
	p.mu.Lock()
	p.due[w.Path] = deadlines
	p.mu.Unlock()
	return f, nil
}

// index matches snap's workspaces against the stored pull requests, built
// once per snapshot and change-log generation.
func (p *Provider) index(ctx context.Context, snap *workspace.Snapshot, cfg config.Config) (*index, error) {
	var key indexKey
	if p.d.Changes != nil {
		key = indexKey{snap.Gen, p.d.Changes.Gen()}
		p.mu.Lock()
		ix := p.ix
		hit := ix != nil && p.ixAt == key
		p.mu.Unlock()
		if hit {
			return ix, nil
		}
	}
	rows, err := p.d.PRs.Rows(ctx)
	if err != nil {
		return nil, err
	}
	overrides, err := p.d.Overrides.Overrides(ctx)
	if err != nil {
		return nil, err
	}
	viewer, err := p.d.PRs.Viewer(ctx)
	if err != nil {
		return nil, err
	}
	ix := newIndex(snap, rows, overrides, viewer, cfg)
	if p.d.Changes != nil {
		p.mu.Lock()
		p.ix, p.ixAt = ix, key
		p.mu.Unlock()
	}
	return ix, nil
}

func (p *Provider) workspace(ctx context.Context, u unit.Unit) (*workspace.Snapshot, workspace.Workspace, error) {
	if u.Provider != unit.Workspace {
		return nil, workspace.Workspace{}, fmt.Errorf("%s is not a workspace", u)
	}
	snap, err := p.read(ctx)
	if err != nil {
		return nil, workspace.Workspace{}, err
	}
	i := slices.IndexFunc(snap.Workspaces, func(w workspace.Workspace) bool { return w.Path == u.Key })
	switch {
	case i >= 0:
		return snap, snap.Workspaces[i], nil
	case snap.WorkspaceScope.Complete:
		return nil, workspace.Workspace{}, fmt.Errorf("workspace %s: %w", u.Key, automation.ErrUnitGone)
	}
	return nil, workspace.Workspace{}, fmt.Errorf("workspace %s is not in an incomplete listing", u.Key)
}

// Gain is the disk the workspace's checkout holds, walked for at most
// GainWalk and kept until its fingerprint changes.
func (p *Provider) Gain(ctx context.Context, u unit.Unit) (workflow.Gain, error) {
	ctx, sc := meter.Start(ctx, Scope)
	defer sc.End()
	_, w, err := p.workspace(ctx, u)
	if err != nil {
		return workflow.Gain{}, err
	}
	if !w.Local() || !p.d.Disk.Exists(w.Path) {
		return workflow.Gain{}, nil
	}
	head, err := p.d.Git.Head(ctx, w.Path)
	if err != nil {
		return workflow.Gain{}, err
	}
	fp := fingerprint(w, head)
	p.c.mu.Lock()
	g, ok := p.c.gain[w.Path]
	p.c.mu.Unlock()
	if ok && g.fingerprint == fp {
		return g.gain, nil
	}
	walk, cancel := context.WithTimeout(ctx, GainWalk)
	defer cancel()
	size, err := p.d.Disk.Size(walk, w.Path)
	if err != nil && walk.Err() == nil {
		return workflow.Gain{}, err
	}
	gain := workflow.Gain{DiskBytes: size}
	p.c.mu.Lock()
	p.c.gain[w.Path] = gained{fp, gain}
	p.c.mu.Unlock()
	return gain, nil
}

// Seen is the change log's generation when List last read the snapshot.
func (p *Provider) Seen() port.Generation {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.seen
}

// Due is the earliest deadline still ahead among the workspaces whose facts
// were last read: the end of cleanup.workspace_grace, in working time, for a
// finished pull request's workspace, and last_activity plus
// cleanup.stale_workspace.
func (p *Provider) Due() time.Time {
	now := p.d.Clock.Now()
	p.mu.Lock()
	defer p.mu.Unlock()
	var due time.Time
	for _, ds := range p.due {
		for _, d := range ds {
			if d.After(now) && (due.IsZero() || d.Before(due)) {
				due = d
			}
		}
	}
	return due
}
