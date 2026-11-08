package tasks

import "github.com/wloophq/wloop/internal/domain/task"

// Handles says whether a handler is registered for verb on e.
func (s *Service) Handles(e task.Task, verb task.Verb) bool { return s.handler(e, verb) != nil }

// Me is the user's identity Sync reconciles with; it never waits on a verb or a sync.
func (s *Service) Me() task.Me {
	return *s.me.Load()
}

// SetMe replaces the user's identity the next Sync reconciles with.
func (s *Service) SetMe(me task.Me) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.d.Me = me
	s.me.Store(&me)
}
