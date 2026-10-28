// Package factsession puts the agent sessions behind the engine's Units: the
// open sessions of the turn registry, active or dormant, with their facts in
// slot session.
package factsession

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"sync"
	"time"

	"github.com/wloophq/wloop/internal/config"
	"github.com/wloophq/wloop/internal/core/automation"
	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/core/sweep"
	"github.com/wloophq/wloop/internal/core/turns"
	"github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/workflow"
)

// UnitStates reads the state of units as their sources last reported it,
// such as a pull request's OPEN or MERGED; a unit with none is absent.
type UnitStates interface {
	States(ctx context.Context, units []unit.Unit) (map[unit.Unit]string, error)
}

type Deps struct {
	// Turns is the turn service: its Registry, Store, Sessions, Intents and
	// Prompts are read, and Refresh writes through it.
	Turns    *turns.Service
	Snapshot sweep.Snapshot
	States   UnitStates
	// Repo is the repository a unit's work is in, "owner/name"; nil, or ""
	// for a unit in none.
	Repo func(unit.Unit) string
	// Changes is the store's change log: one read of the world serves every
	// call until it or the snapshot moves; nil reads it on every call.
	Changes port.Changes
	// Exists says whether a path exists; nil stats it.
	Exists func(path string) bool
	// Transcript is the transcript file the hooks stored for an agent
	// session, "" when none is known; nil knows none.
	Transcript func(ctx context.Context, agentSession string) (string, error)
	// ClosedByManager says whether the session's manager keeps a record of it
	// that the user closed; nil knows none.
	ClosedByManager func(ctx context.Context, manager, session, lastHandle string) (bool, error)
	Cfg             func() config.Config
	Clock           port.Clock
}

type Provider struct {
	d Deps

	mu     sync.Mutex
	due    map[string]time.Time
	cached *world
}

var (
	_ automation.Units     = (*Provider)(nil)
	_ automation.Refresher = (*Provider)(nil)
	_ automation.Dueer     = (*Provider)(nil)
	_ automation.Owner     = (*Provider)(nil)
)

func New(d Deps) *Provider {
	if d.Exists == nil {
		d.Exists = func(path string) bool {
			_, err := os.Stat(path)
			return !errors.Is(err, os.ErrNotExist)
		}
	}
	return &Provider{d: d, due: map[string]time.Time{}}
}

// Refresh keeps the idle periods, takes every agent process no session took
// as a session, and moves the census marks that make a process gone.
func (p *Provider) Refresh(ctx context.Context) error {
	if err := p.d.Turns.KeepSessions(ctx); err != nil {
		return err
	}
	return p.d.Turns.Census(ctx)
}

func (p *Provider) List(ctx context.Context, id unit.ProviderID) ([]unit.Unit, error) {
	if id != unit.Session {
		return nil, fmt.Errorf("the session provider has no %s units", id)
	}
	w, err := p.read(ctx)
	if err != nil {
		return nil, err
	}
	handles := w.ids()
	due := make(map[string]time.Time, len(handles))
	out := make([]unit.Unit, 0, len(handles))
	for _, h := range handles {
		s := w.state(h)
		due[h] = p.deadline(s)
		out = append(out, unit.Unit{Provider: unit.Session, Key: h})
	}
	p.mu.Lock()
	p.due = due
	p.mu.Unlock()
	slices.SortFunc(out, func(a, b unit.Unit) int {
		switch {
		case a.Less(b):
			return -1
		case b.Less(a):
			return 1
		}
		return 0
	})
	return out, nil
}

func (p *Provider) Facts(ctx context.Context, u unit.Unit) (workflow.Facts, error) {
	if u.Provider != unit.Session {
		return nil, fmt.Errorf("%s: %w", u.Label(), automation.ErrUnitGone)
	}
	w, err := p.read(ctx)
	if err != nil {
		return nil, err
	}
	if !slices.Contains(w.ids(), u.Key) {
		return nil, p.missing(ctx, w, u.Key)
	}
	s := w.state(u.Key)
	pf, err := p.d.Turns.PromptFacts(ctx, s.sess, s.agent)
	if err != nil {
		return nil, err
	}
	p.mu.Lock()
	p.due[u.Key] = p.deadline(s)
	p.mu.Unlock()
	return s.facts(p.d.Clock.Now(), pf, p.d.Turns.ProcessGone(u.Key)), nil
}

// Owners is the unit the session's facts are about: that of the newest turn
// it ran or was sent; none when it has no turn.
func (p *Provider) Owners(ctx context.Context, u unit.Unit) ([]unit.Unit, error) {
	if u.Provider != unit.Session {
		return nil, nil
	}
	units, err := p.d.Turns.Store.UnitsOf(ctx, []string{u.Key})
	if err != nil {
		return nil, err
	}
	if owner, ok := units[u.Key]; ok {
		return []unit.Unit{owner}, nil
	}
	return nil, nil
}

// missing is why a session the world does not list has no facts: it is
// closed, or no registry ever held it.
func (p *Provider) missing(ctx context.Context, _ *world, id string) error {
	if _, _, err := p.d.Turns.Registry.Session(ctx, id); err != nil {
		return err
	}
	return fmt.Errorf("session %s: %w", id, automation.ErrUnitGone)
}

// Due is the earliest moment a session crosses a cleanup threshold, or a
// census mark can make one gone.
func (p *Provider) Due() time.Time {
	now := p.d.Clock.Now()
	next := p.d.Turns.NextGone()
	if !next.After(now) {
		next = time.Time{}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, at := range p.due {
		if at.After(now) && (next.IsZero() || at.Before(next)) {
			next = at
		}
	}
	return next
}

// deadline is when the session's idle period crosses the threshold of the
// cleanup workflow it is subject to, zero when none applies.
func (p *Provider) deadline(s *state) time.Time {
	if s.idleSince.IsZero() {
		return time.Time{}
	}
	c := p.d.Cfg().Cleanup
	var after config.Days
	switch {
	case s.sess.Managed && s.newest != nil && slices.Contains(quietStates, s.newest.State):
		after = c.Idle
	case s.sess.Managed && s.newest != nil && s.newest.State == turn.Asked:
		after = c.AskedIdle
	case !s.sess.Managed && s.hasAgent && s.agent.State == turn.AgentDone:
		after = c.ManualIdle
	default:
		return time.Time{}
	}
	return s.idleSince.Add(time.Duration(after))
}
