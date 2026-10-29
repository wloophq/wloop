package tasks

import "github.com/wloophq/wloop/internal/domain/task"

// Handles says whether a handler is registered for verb on e.
func (s *Service) Handles(e task.Task, verb task.Verb) bool { return s.handler(e, verb) != nil }

// SetMe replaces the user's identity the next Sync reconciles with.
func (s *Service) SetMe(me task.Me) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.d.Me = me
}
