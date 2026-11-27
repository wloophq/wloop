package tasks

import (
	"context"
	"sync"
	"time"

	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/meter"
)

// PassIgnores are the topics no fact and no load of a pass reads: a note of them alone leaves the shared pass
// current. A topic left out of it costs a new pass, never a stale answer, so it lists only what is known.
const PassIgnores = port.TopicApp | port.TopicUsage | port.TopicJobs | port.TopicViews

// passReuse bounds how long a pass serves the reads that start while the store's generation holds, so a
// fact a write forgets to note, or one that moves with the clock, is late by no more than it.
const passReuse = 2 * time.Second

// sharedPass is the last pass the service read and the change log's generation before its tasks were read.
type sharedPass struct {
	mu       sync.Mutex
	pass     *Pass
	gen      port.Generation
	at       time.Time
	building chan struct{}
}

// pass is a pass over every stored task: the one ctx carries when it holds every task, else the shared one while
// no note but of PassIgnores followed it and it is younger than passReuse, else a new one, which the reads that
// start while it is read wait for and share.
func (s *Service) pass(ctx context.Context) (*Pass, error) {
	if p, ok := PassOf(ctx); ok && p.all {
		return p, nil
	}
	if s.d.Changes == nil {
		return s.newPass(ctx)
	}
	sh := &s.shared
	for {
		sh.mu.Lock()
		gen, now := s.d.Changes.Gen(), s.d.Clock.Now()
		young := sh.pass != nil && now.Sub(sh.at) < passReuse
		if young && (sh.gen == gen || s.d.Changes.Since(sh.gen, port.AllTopics&^PassIgnores).Empty()) {
			p := sh.pass
			sh.gen = gen
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
		if young {
			s.missed(ctx, sh.gen)
		}
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
	return &Pass{Tasks: all, all: true}, nil
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

// missed counts a shared pass, still young at gen, that a note left behind, once per topic the notes since moved
// that the pass reads.
func (s *Service) missed(ctx context.Context, gen port.Generation) {
	for _, name := range s.d.Changes.Since(gen, port.AllTopics&^PassIgnores).Topics.Names() {
		meter.Add(ctx, meter.Sample{Res: meter.Work, Op: "pass missed: " + name})
	}
}
