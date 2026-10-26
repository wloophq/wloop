package cli

import (
	"context"
	"slices"
	"sync"

	"github.com/wloophq/wloop/internal/core/messages"
	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/core/turns"
	dgh "github.com/wloophq/wloop/internal/domain/github"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
)

type storedPRs interface {
	PRs(ctx context.Context, refs []pr.Ref) (map[pr.Ref]dinv.PR, error)
}

// openPRBooks are the turn books with an empty book for each pull request
// that merged or closed, so its question and agent posts close as
// superseded.
type openPRBooks struct {
	books messages.Books
	prs   storedPRs
}

func (b openPRBooks) BookOf(ctx context.Context, k turns.Key) (turn.Book, bool, error) {
	return b.books.BookOf(ctx, k)
}

func (b openPRBooks) Books(ctx context.Context, units ...unit.Unit) (map[unit.Unit]turn.Book, error) {
	books, err := b.books.Books(ctx, units...)
	if err != nil {
		return nil, err
	}
	var refs []pr.Ref
	for u := range books {
		if ref, ok := pr.RefOf(u); ok {
			refs = append(refs, ref)
		}
	}
	if len(refs) == 0 {
		return books, nil
	}
	stored, err := b.prs.PRs(ctx, refs)
	if err != nil {
		return nil, err
	}
	for ref, p := range stored {
		if p.State != dgh.StateOpen {
			books[ref.Unit()] = turn.Book{}
		}
	}
	return books, nil
}

// reconcileEnded reconciles the messages of the pull requests each
// TopicPRs note names that hold an open message, until ctx ends.
func reconcileEnded(ctx context.Context, s *messages.Service, changes port.Changes, logf func(string, ...any)) {
	var mu sync.Mutex
	var noted []unit.Unit
	kick := make(chan struct{}, 1)
	cancel := changes.Subscribe(func(c port.Change) {
		if !c.Has(port.TopicPRs) || c.All {
			return
		}
		mu.Lock()
		noted = append(noted, c.Units...)
		mu.Unlock()
		select {
		case kick <- struct{}{}:
		default:
		}
	})
	defer cancel()
	for {
		select {
		case <-ctx.Done():
			return
		case <-kick:
		}
		mu.Lock()
		todo := noted
		noted = nil
		mu.Unlock()
		open, err := s.Store.Open(ctx)
		if err != nil {
			logf("messages: the open messages: %v", err)
			continue
		}
		var units []unit.Unit
		for _, m := range open {
			if slices.Contains(todo, m.Unit) && !slices.Contains(units, m.Unit) {
				units = append(units, m.Unit)
			}
		}
		if len(units) == 0 {
			continue
		}
		if err := s.Reconcile(ctx, units...); err != nil {
			logf("messages: reconciling %d ended pull requests: %v", len(units), err)
		}
	}
}
