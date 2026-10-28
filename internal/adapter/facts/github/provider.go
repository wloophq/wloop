// Package factsgithub is the github source's workflow facts: the pull
// requests a sweep plans over, as automation.Units, and their author
// facts, as views.Facts. It reads the store, the workspace snapshot and the
// local checkouts, and never asks GitHub.
package factsgithub

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/wloophq/wloop/internal/config"
	"github.com/wloophq/wloop/internal/core/automation"
	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/core/sweep"
	"github.com/wloophq/wloop/internal/core/views"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/round"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/workflow"
)

// The rounds a Plan names.
const (
	RoundFollowup = "follow-up"
	RoundFirst    = "first"
	RoundAddress  = "address"
)

// Store is what the facts read of the inventory besides the planning inputs,
// and the events Refresh and Settle record.
type Store interface {
	Overrides(ctx context.Context) (map[pr.Ref]dinv.Override, error)
	// FirstRoundBlocks maps each unit whose round is blocked to its
	// round_blocked event.
	FirstRoundBlocks(ctx context.Context) (map[unit.Unit]port.Event, error)
	EventsByPR(ctx context.Context, refs []pr.Ref) (map[pr.Ref][]dinv.Event, error)
	RecordEvent(ctx context.Context, ev dinv.Event) error
}

// Branches reads what a first round's review workspace would find of the
// pull request's head branch in the local checkout.
type Branches interface {
	LocalBranchTip(ctx context.Context, dir, branch string) (string, error)
	CheckedOutAt(ctx context.Context, dir, branch string) (string, error)
	HasCommit(ctx context.Context, dir, oid string) (bool, error)
	IsAncestor(ctx context.Context, dir, ancestor, commit string) (bool, error)
}

// RoundState is the sweep's round state as Settle receives it.
type RoundState interface {
	automation.State
	Rounds() *round.Store
}

// Deps are what the provider reads. Changes, Teams and Intents may be
// nil: without Changes every call reads the store again.
type Deps struct {
	Inputs   dinv.PlanInputs
	Store    Store
	Snapshot sweep.Snapshot
	Books    sweep.Books
	Rounds   sweep.Rounds
	Git      sweep.LocalGit
	Branches Branches
	Cfg      func() config.Config
	// Sink is the view sink whose columns the workspaces report as statuses.
	Sink    string
	Clock   port.Clock
	Changes port.Changes
	Teams   func(ctx context.Context) ([]string, error)
	Intents func() *dintent.Registry
}

// Provider is the github source's facts.
type Provider struct {
	d Deps

	mu   sync.Mutex
	view *view
	dues map[string]time.Time
	revs revs
}

var (
	_ automation.Units   = (*Provider)(nil)
	_ automation.Settler = (*Provider)(nil)
	_ automation.Dueer   = (*Provider)(nil)
	_ views.Facts        = (*Provider)(nil)
)

func New(d Deps) *Provider {
	return &Provider{d: d, dues: map[string]time.Time{}, revs: revs{byUnit: map[unit.Unit]uint64{}}}
}

var errNotGitHub = errors.New("not a pull request")

func refOf(u unit.Unit) (pr.Ref, error) {
	ref, ok := pr.RefOf(u)
	if !ok {
		return pr.Ref{}, fmt.Errorf("%s: %w", u.Label(), errNotGitHub)
	}
	return ref, nil
}

// List is every unit a workflow over pull requests may act on: those a
// sweep plans over, the open ones nothing lapsed, the ones with an open
// turn and the ones with an answer waiting.
func (p *Provider) List(ctx context.Context, id unit.ProviderID) ([]unit.Unit, error) {
	if id != unit.GitHub {
		return nil, nil
	}
	v, err := p.fresh(ctx, true)
	if err != nil {
		return nil, err
	}
	now := p.now()
	units := v.listed(ctx, now)
	if err := v.loadFacts(ctx, units); err != nil {
		return nil, err
	}
	if err := v.loadEvents(ctx, units); err != nil {
		return nil, err
	}
	pl := v.planner(ctx, now)
	ranks := make(map[unit.Unit]rank, len(units))
	dues := map[string]time.Time{}
	for _, u := range units {
		ref, _ := refOf(u)
		r, err := pl.derive(ref)
		if err != nil {
			return nil, err
		}
		ranks[u] = pl.rank(ref, r)
		for k, t := range r.dues {
			dues[k] = earliestOf(dues[k], t)
		}
		if r.runs() && v.rounds.CapReason(ref.String(), now, v.cfg.Turns.PerUnitPerDay) != "" {
			dues["cap"] = nextDay(now)
		}
	}
	p.mu.Lock()
	p.dues = dues
	p.mu.Unlock()
	slices.SortStableFunc(units, func(a, b unit.Unit) int { return ranks[a].compare(ranks[b]) })
	return units, nil
}

// Facts are the pr facts of u; a pull request the store does not hold yet
// has those that need no row.
func (p *Provider) Facts(ctx context.Context, u unit.Unit) (workflow.Facts, error) {
	ref, err := refOf(u)
	if err != nil {
		return nil, err
	}
	v, err := p.fresh(ctx, false)
	if err != nil {
		return nil, err
	}
	if err := v.loadFacts(ctx, []unit.Unit{u}); err != nil {
		return nil, err
	}
	if err := v.loadEvents(ctx, []unit.Unit{u}); err != nil {
		return nil, err
	}
	pl := v.planner(ctx, p.now())
	r, err := pl.derive(ref)
	if err != nil {
		return nil, err
	}
	return pl.facts(ref, r), nil
}

// Due is the earliest instant after now a decision of the last evaluation
// changes with no input changing.
func (p *Provider) Due() time.Time {
	now := p.now()
	p.mu.Lock()
	defer p.mu.Unlock()
	var due time.Time
	for _, t := range p.dues {
		if t.After(now) {
			due = earliestOf(due, t)
		}
	}
	return due
}

func (p *Provider) now() time.Time { return p.d.Clock.Now() }

func (p *Provider) cfg() config.Config {
	if p.d.Cfg == nil {
		return config.Defaults()
	}
	return p.d.Cfg()
}

// viewTopics are the inputs whose notes make the cached reads stale.
const viewTopics = port.TopicPRs | port.TopicFollow | port.TopicSourceEvents | port.TopicLoopEvents | port.TopicOverrides |
	port.TopicTurns | port.TopicRounds | port.TopicPause | port.TopicAgents | port.TopicConfig | port.TopicViewer | port.TopicMeta

// maxViewAge bounds how long reads no change log note covers, the checkouts'
// branches, are reused.
const maxViewAge = time.Minute

// fresh is the cached view while no note made it stale, else a new one;
// renew builds a new one regardless, as each evaluation's List does.
func (p *Provider) fresh(ctx context.Context, renew bool) (*view, error) {
	now := p.now()
	p.mu.Lock()
	v := p.view
	p.mu.Unlock()
	if !renew && v != nil && p.d.Changes != nil && now.Sub(v.builtAt) < maxViewAge && p.d.Changes.Since(v.gen, viewTopics).Empty() {
		return v, nil
	}
	v, err := p.build(ctx, now)
	if err != nil {
		return nil, err
	}
	p.mu.Lock()
	p.view = v
	p.mu.Unlock()
	return v, nil
}

func nextDay(t time.Time) time.Time {
	y, m, d := t.Local().Date()
	return time.Date(y, m, d+1, 0, 0, 0, 0, time.Local)
}

func earliestOf(a, b time.Time) time.Time {
	switch {
	case a.IsZero():
		return b
	case b.IsZero() || a.Before(b):
		return a
	}
	return b
}
