// Package factsgithub is the github source's workflow facts: the pull
// requests a sweep plans over, as automation.Units, and their author
// facts, as views.Facts. It reads the store, the workspace snapshot and the
// local checkouts; only Moved asks GitHub.
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
	"github.com/wloophq/wloop/internal/core/turns"
	"github.com/wloophq/wloop/internal/core/views"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/round"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/workflow"
	wsdomain "github.com/wloophq/wloop/internal/domain/workspace"
	"github.com/wloophq/wloop/internal/github"
)

// The rounds a Candidate names.
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

// StubReader reads pull requests' stubs from GitHub in one batched read.
type StubReader interface {
	StubsByRef(ctx context.Context, refs []github.Ref) (map[github.Ref]github.Stub, map[github.Ref]error, error)
}

// RoundState is the sweep's round state as Settle receives it.
type RoundState interface {
	automation.State
	Rounds() *round.Store
}

// Deps are what the provider reads. Changes, Teams, Agents and Intents may be
// nil: without Changes every call reads the store again.
type Deps struct {
	Inputs   sweep.Inputs
	Store    Store
	Snapshot sweep.Snapshot
	Books    sweep.Books
	Rounds   sweep.Rounds
	Git      sweep.LocalGit
	Branches Branches
	Stubs    StubReader
	Cfg      func() config.Config
	// Sink is the view sink whose columns the workspaces report as statuses.
	Sink    string
	Clock   port.Clock
	Changes port.Changes
	Teams   func(ctx context.Context) ([]string, error)
	// Agents are the agents by name, for the answer a start block waits on.
	Agents  map[string]turns.Agent
	Intents func() *dintent.Registry
}

// Candidate is what a round is planned with.
type Candidate struct {
	Round        string
	Workspace    wsdomain.Workspace
	Facts        sweep.Facts
	Fingerprint  string
	ChecksHead   string
	ConflictHead string
	LastReviewAt time.Time
	Observed     bool
	// Reason is why the planner plans the round.
	Reason string
}

// Provider is the github source's facts.
type Provider struct {
	d Deps

	mu   sync.Mutex
	view *view
	dues map[string]time.Time
}

var (
	_ automation.Units     = (*Provider)(nil)
	_ automation.Refresher = (*Provider)(nil)
	_ automation.Settler   = (*Provider)(nil)
	_ automation.Freshener = (*Provider)(nil)
	_ automation.Dueer     = (*Provider)(nil)
	_ views.Facts          = (*Provider)(nil)
)

// The deadlines a sweep's evaluation does not recompute: a moved stub's
// next fast run and a blind start block's day.
const (
	dueMoved = "moved"
	dueBlock = "block"
)

func New(d Deps) *Provider { return &Provider{d: d, dues: map[string]time.Time{}} }

var errNotGitHub = errors.New("not a pull request")

func refOf(u unit.Unit) (pr.Ref, error) {
	ref, ok := port.PROf(port.Event{Unit: u})
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
		if r.runs() && v.rounds.CapReason(ref.String(), now, v.cfg.Rounds.PerPRPerDay) != "" {
			dues["cap"] = nextDay(now)
		}
	}
	p.mu.Lock()
	for _, k := range []string{dueMoved, dueBlock} {
		if p.dues[k].After(now) {
			dues[k] = earliestOf(dues[k], p.dues[k])
		}
	}
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

// Candidate is the round of u the facts plan, with what its turn is sent
// with; false when that round is not due.
func (p *Provider) Candidate(ctx context.Context, u unit.Unit, round string) (Candidate, bool, error) {
	ref, err := refOf(u)
	if err != nil {
		return Candidate{}, false, err
	}
	v, err := p.fresh(ctx, false)
	if err != nil {
		return Candidate{}, false, err
	}
	if err := v.loadFacts(ctx, []unit.Unit{u}); err != nil {
		return Candidate{}, false, err
	}
	r, err := v.planner(ctx, p.now()).derive(ref)
	if err != nil {
		return Candidate{}, false, err
	}
	c := Candidate{Round: round}
	c.Facts, _ = v.factsOf(ref)
	switch round {
	case RoundFollowup:
		if r.review.action != actionFollowup {
			return Candidate{}, false, nil
		}
		c.Workspace, c.Fingerprint, c.LastReviewAt, c.Observed, c.Reason = r.review.ws, r.review.fingerprint, r.review.lastReviewAt, r.review.observed, r.review.reason
	case RoundFirst:
		if !r.first.due {
			return Candidate{}, false, nil
		}
		c.Reason = r.first.reason
	case RoundAddress:
		if !r.address.due {
			return Candidate{}, false, nil
		}
		c.Workspace, c.Fingerprint, c.ChecksHead, c.ConflictHead, c.Observed, c.Reason = r.address.ws, r.address.fingerprint, r.address.checksHead, r.address.conflictHead, r.address.observed, r.address.reason
	default:
		return Candidate{}, false, fmt.Errorf("no round %q of a pull request", round)
	}
	return c, true, nil
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

func (p *Provider) dueAt(key string, t time.Time) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.dues[key].After(p.now()) {
		delete(p.dues, key)
	}
	p.dues[key] = earliestOf(p.dues[key], t)
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
