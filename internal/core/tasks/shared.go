package tasks

import (
	"context"
	"sync"
	"time"

	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/domain/task"
)

// passReuse bounds how long a pass serves the reads that start while the store's generation holds, so a
// fact a write forgets to note, or one that moves with the clock, is late by no more than it.
const passReuse = 2 * time.Second

// sharedPass is the last pass the service read and the store's generation before its tasks were read.
type sharedPass struct {
	mu       sync.Mutex
	pass     *Pass
	gen      port.Generation
	at       time.Time
	building chan struct{}
}

// pass is a pass over every stored task: the shared one while the store's generation holds and it is younger
// than passReuse, else a new one, which the reads that start while it is read wait for and share.
func (s *Service) pass(ctx context.Context) (*Pass, error) {
	if s.d.Gen == nil {
		return s.newPass(ctx)
	}
	sh := &s.shared
	for {
		sh.mu.Lock()
		gen, now := s.d.Gen(), s.d.Clock.Now()
		if sh.pass != nil && sh.gen == gen && now.Sub(sh.at) < passReuse {
			p := sh.pass
			sh.mu.Unlock()
			return p, nil
		}
		if wait := sh.building; wait != nil {
			sh.mu.Unlock()
			select {
			case <-wait:
				continue
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		done := make(chan struct{})
		sh.building = done
		sh.mu.Unlock()
		p, err := s.newPass(ctx)
		sh.mu.Lock()
		sh.building = nil
		if err == nil {
			sh.pass, sh.gen, sh.at = p, gen, now
		}
		sh.mu.Unlock()
		close(done)
		return p, err
	}
}

func (s *Service) newPass(ctx context.Context) (*Pass, error) {
	all, err := s.d.Store.Tasks(ctx, task.Query{All: true})
	if err != nil {
		return nil, err
	}
	return &Pass{Tasks: all}, nil
}

// covers says whether p holds every task of a page as the page read it, and, for a full page, no other.
func (p *Pass) covers(page Page) bool {
	if page.Full && len(page.Tasks) != len(p.Tasks) {
		return false
	}
	for _, e := range page.Tasks {
		have, ok := p.Task(e.ID)
		if !ok || have.State != e.State || !have.Updated.Equal(e.Updated) {
			return false
		}
	}
	return true
}
