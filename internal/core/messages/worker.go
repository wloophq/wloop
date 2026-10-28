package messages

import (
	"context"
	"slices"
	"sync"

	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/domain/unit"
)

// Run reconciles every book and proposal, then the units of each TopicTurns and TopicTasks note, so a gate
// leaves a task that closed, and the proposals on each TopicItems note, on its own goroutine, until ctx ends:
// the subscriber only queues them.
func (s *Service) Run(ctx context.Context, changes port.Changes) {
	var mu sync.Mutex
	var units []unit.Unit
	all, proposals := true, true
	kick := make(chan struct{}, 1)
	cancel := changes.Subscribe(func(c port.Change) {
		turnsNoted, itemsNoted, tasksNoted := c.Has(port.TopicTurns), c.Has(port.TopicItems), c.Has(port.TopicTasks) && !c.All
		if !turnsNoted && !itemsNoted && !tasksNoted {
			return
		}
		mu.Lock()
		if turnsNoted {
			all = all || c.All
		}
		if turnsNoted || tasksNoted {
			units = append(units, c.Units...)
		}
		proposals = proposals || itemsNoted
		mu.Unlock()
		select {
		case kick <- struct{}{}:
		default:
		}
	})
	defer cancel()
	for {
		mu.Lock()
		todo, everything, props := units, all, proposals
		units, all, proposals = nil, false, false
		mu.Unlock()
		s.sweep(ctx, todo, everything, props)
		select {
		case <-ctx.Done():
			return
		case <-kick:
		}
	}
}

func (s *Service) sweep(ctx context.Context, units []unit.Unit, all, proposals bool) {
	switch {
	case all:
		if err := s.Reconcile(ctx); err != nil {
			s.logf("messages: reconciling the questions: %v", err)
		}
	case len(units) > 0:
		slices.SortFunc(units, func(a, b unit.Unit) int {
			switch {
			case a.Less(b):
				return -1
			case b.Less(a):
				return 1
			}
			return 0
		})
		if err := s.Reconcile(ctx, slices.Compact(units)...); err != nil {
			s.logf("messages: reconciling the questions of %d units: %v", len(units), err)
		}
	}
	if proposals {
		if err := s.ReconcileProposals(ctx); err != nil {
			s.logf("messages: reconciling the proposals: %v", err)
		}
	}
}
