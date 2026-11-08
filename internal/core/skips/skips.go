// Package skips keeps the workflows the user stopped on one unit: the workflow plans nothing there until the
// user undoes it, and its drafts held there are withdrawn.
package skips

import (
	"context"
	"slices"
	"sync"
	"time"

	"github.com/wloophq/wloop/internal/core/outbox"
	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/unit"
)

// Store keeps the skips.
type Store interface {
	SaveSkip(ctx context.Context, workflow string, u unit.Unit, at time.Time) error
	DeleteSkip(ctx context.Context, workflow string, u unit.Unit) error
	AllSkips(ctx context.Context) (map[unit.Unit][]string, error)
}

// Drafts are the Outbox's drafts a skip withdraws.
type Drafts interface {
	List(ctx context.Context, f outbox.Filter) ([]draft.Draft, error)
	Withdraw(ctx context.Context, id draft.ID, reason string) (draft.Draft, error)
}

// Service skips workflows on units. It reads the store once and keeps the skips in memory after, since it is
// their only writer.
type Service struct {
	Store  Store
	Drafts Drafts
	Clock  port.Clock

	mu    sync.Mutex
	byU   map[unit.Unit][]string
	ready bool
}

// Skip stops workflow on u, then withdraws the workflow's drafts held on u.
func (s *Service) Skip(ctx context.Context, workflow string, u unit.Unit) error {
	s.mu.Lock()
	err := s.load(ctx)
	if err == nil {
		err = s.Store.SaveSkip(ctx, workflow, u, s.Clock.Now())
	}
	if err == nil && !slices.Contains(s.byU[u], workflow) {
		s.byU[u] = append(s.byU[u], workflow)
	}
	s.mu.Unlock()
	if err != nil || s.Drafts == nil {
		return err
	}
	held, err := s.Drafts.List(ctx, outbox.Filter{Unit: &u, Workflow: workflow, States: []draft.State{draft.Held}})
	if err != nil {
		return err
	}
	for _, d := range held {
		if _, err := s.Drafts.Withdraw(ctx, d.ID, draft.WithdrawSkipped); err != nil {
			return err
		}
	}
	return nil
}

// Unskip lets workflow run on u again.
func (s *Service) Unskip(ctx context.Context, workflow string, u unit.Unit) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.load(ctx); err != nil {
		return err
	}
	if err := s.Store.DeleteSkip(ctx, workflow, u); err != nil {
		return err
	}
	s.byU[u] = slices.DeleteFunc(s.byU[u], func(w string) bool { return w == workflow })
	if len(s.byU[u]) == 0 {
		delete(s.byU, u)
	}
	return nil
}

// Skipped says whether workflow is skipped on u.
func (s *Service) Skipped(ctx context.Context, workflow string, u unit.Unit) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.load(ctx); err != nil {
		return false, err
	}
	return slices.Contains(s.byU[u], workflow), nil
}

// Of are the workflows skipped on u, by name.
func (s *Service) Of(ctx context.Context, u unit.Unit) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.load(ctx); err != nil {
		return nil, err
	}
	out := slices.Clone(s.byU[u])
	slices.Sort(out)
	return out, nil
}

func (s *Service) load(ctx context.Context) error {
	if s.ready {
		return nil
	}
	all, err := s.Store.AllSkips(ctx)
	if err != nil {
		return err
	}
	if all == nil {
		all = map[unit.Unit][]string{}
	}
	s.byU, s.ready = all, true
	return nil
}
