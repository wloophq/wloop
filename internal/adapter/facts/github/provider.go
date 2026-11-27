// Package factsgithub is the github source's workflow facts: the pull
// requests a sweep plans over, as automation.Units, and their author
// facts, as views.Facts. It lays the host's own state over what the GitHub
// plugin reports, its planner's facts among it, and never asks GitHub.
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
	coretasks "github.com/wloophq/wloop/internal/core/tasks"
	"github.com/wloophq/wloop/internal/core/views"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/workflow"
	"github.com/wloophq/wloop/internal/readmodel"
	"github.com/wloophq/wloop/internal/readmodel/prrow"
)

// Store is what the facts read of the inventory besides the planning
// inputs.
type Store interface {
	Overrides(ctx context.Context) (map[pr.Ref]dinv.Override, error)
	EventsByPR(ctx context.Context, refs []pr.Ref) (map[pr.Ref][]dinv.Event, error)
}

// Deps are what the provider reads. Changes and Intents may be nil:
// without Changes every call reads the store again.
type Deps struct {
	Inputs   dinv.PlanInputs
	Store    Store
	Snapshot sweep.Snapshot
	Books    sweep.Books
	Rounds   sweep.Rounds
	Git      sweep.LocalGit
	Cfg      func() config.Config
	Clock    port.Clock
	Changes  port.Changes
	Intents  func() *dintent.Registry
	// Reports are what the GitHub plugin last reported of its pull
	// requests: their situations, the facts that read them alone and its
	// planner's; nil reports none.
	Reports readmodel.Reports
	// Mutes are the user's mutes of pull requests, their tasks' wakes; nil
	// mutes none.
	Mutes readmodel.Mutes
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
	_ automation.Units = (*Provider)(nil)
	_ automation.Dueer = (*Provider)(nil)
	_ views.Facts      = (*Provider)(nil)
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
	units, err := v.listed(ctx, now)
	if err != nil {
		return nil, err
	}
	if err := v.loadEvents(ctx, units); err != nil {
		return nil, err
	}
	if err := v.loadReports(ctx, units); err != nil {
		return nil, err
	}
	dues := map[string]time.Time{}
	for _, u := range units {
		ref, _ := refOf(u)
		if runs(v.facts(ref, now)) && v.rounds.CapReason(ref.String(), now, v.cfg.Turns.PerUnitPerDay) != "" {
			dues["cap"] = nextDay(now)
		}
	}
	p.mu.Lock()
	p.dues = dues
	p.mu.Unlock()
	slices.SortStableFunc(units, func(a, b unit.Unit) int { return v.rank(a).Compare(v.rank(b)) })
	return units, nil
}

// rank is where u's rounds rank, as the plugin's planner reported it.
func (v *view) rank(u unit.Unit) prrow.Rank {
	ref, _ := refOf(u)
	if rep := v.reportOf(ref); rep != nil {
		return rep.Plan.Rank
	}
	return prrow.Rank{Path: 3}
}

// Facts are the pr facts of u; a pull request the store does not hold yet
// has those that need no row.
func (p *Provider) Facts(ctx context.Context, u unit.Unit) (workflow.Facts, error) {
	ref, err := refOf(u)
	if err != nil {
		return nil, err
	}
	v, err := p.passView(ctx)
	if err != nil {
		return nil, err
	}
	if pass, ok := coretasks.PassOf(ctx); ok && !v.loaded(ref) {
		if err := v.primeClosed(ctx, pass); err != nil {
			return nil, err
		}
	}
	if err := v.loadEvents(ctx, []unit.Unit{u}); err != nil {
		return nil, err
	}
	if err := v.loadReports(ctx, []unit.Unit{u}); err != nil {
		return nil, err
	}
	return v.facts(ref, p.now()), nil
}

// passView is the view a unit's facts read in the pass ctx carries, primed with the events and reports of every
// pull request the pass's open tasks link, read at once: in a page, one view for the whole page; outside one,
// such as in the sweep's pass, whose List renews the view between reads, the view each call finds current.
func (p *Provider) passView(ctx context.Context) (*view, error) {
	pass, ok := coretasks.PassOf(ctx)
	if !ok {
		return p.fresh(ctx, false)
	}
	if coretasks.InPage(ctx) {
		return coretasks.PageOnce(ctx, sharedViewKey, func() (*view, error) {
			v, err := p.fresh(ctx, false)
			if err != nil {
				return nil, err
			}
			return v, v.prime(ctx, pass)
		})
	}
	v, err := p.fresh(ctx, false)
	if err != nil {
		return nil, err
	}
	return v, v.prime(ctx, pass)
}

// prime reads the events and reports of every pull request pass's open tasks link into v, once per pass.
func (v *view) prime(ctx context.Context, pass *coretasks.Pass) error {
	v.mu.Lock()
	primed := v.primed == pass
	v.mu.Unlock()
	if primed {
		return nil
	}
	prs, err := coretasks.Once(ctx, openPRsOnce, func() ([]unit.Unit, error) { return linkedPRs(pass, true), nil })
	if err != nil {
		return err
	}
	if err := v.load(ctx, prs); err != nil {
		return err
	}
	v.mu.Lock()
	v.primed = pass
	v.mu.Unlock()
	return nil
}

// primeClosed reads the events and reports of every pull request pass's closed tasks link into v, once per
// pass: the facts of one such pull request are asked for most often by a view over closed tasks, which asks for
// them all.
func (v *view) primeClosed(ctx context.Context, pass *coretasks.Pass) error {
	v.mu.Lock()
	primed := v.closedPrimed == pass
	v.mu.Unlock()
	if primed {
		return nil
	}
	prs, err := coretasks.Once(ctx, closedPRsOnce, func() ([]unit.Unit, error) { return linkedPRs(pass, false), nil })
	if err != nil {
		return err
	}
	if err := v.load(ctx, prs); err != nil {
		return err
	}
	v.mu.Lock()
	v.closedPrimed = pass
	v.mu.Unlock()
	return nil
}

func (v *view) load(ctx context.Context, prs []unit.Unit) error {
	if err := v.loadEvents(ctx, prs); err != nil {
		return err
	}
	return v.loadReports(ctx, prs)
}

// linkedPRs are the pull requests pass's open tasks link, or its closed ones, each once.
func linkedPRs(pass *coretasks.Pass, open bool) []unit.Unit {
	var prs []unit.Unit
	seen := map[unit.Unit]bool{}
	for _, e := range pass.Tasks {
		if e.Open() != open {
			continue
		}
		for _, u := range e.LinkedUnits() {
			if u.Provider == unit.GitHub && !seen[u] {
				seen[u] = true
				prs = append(prs, u)
			}
		}
	}
	return prs
}

const (
	openPRsOnce   = "facts.github.prs"
	closedPRsOnce = "facts.github.closed-prs"
)

const sharedViewKey = "facts.github.view"

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
	port.TopicWakes | port.TopicTurns | port.TopicRounds | port.TopicPause | port.TopicAgents | port.TopicConfig | port.TopicViewer | port.TopicMeta

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
